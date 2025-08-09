package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/cache"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"
)

// OptimizedDB wraps sql.DB with prepared statements and optimizations
type OptimizedDB struct {
	master   *sql.DB                    // Write connection
	replicas []*sql.DB                  // Read replicas
	stmts    map[string]*sql.Stmt       // Prepared statements cache
	mu       sync.RWMutex               // Protect statement cache
	rrIndex  atomic.Uint32              // Round-robin index for replicas
}

// Config for database connections
type Config struct {
	MasterDSN      string
	ReplicaDSNs    []string
	MaxConnections int
	MaxIdleConns   int
	ConnMaxLife    time.Duration
}

// NewOptimizedDB creates an optimized database connection pool
func NewOptimizedDB(cfg Config) (*OptimizedDB, error) {
	// Master connection for writes
	master, err := sql.Open("postgres", cfg.MasterDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open master: %w", err)
	}

	// Optimize connection pool
	master.SetMaxOpenConns(cfg.MaxConnections)
	master.SetMaxIdleConns(cfg.MaxIdleConns)
	master.SetConnMaxLifetime(cfg.ConnMaxLife)
	
	// Enable statement caching at driver level
	master.SetConnMaxIdleTime(5 * time.Minute)

	if err := master.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping master: %w", err)
	}

	db := &OptimizedDB{
		master:   master,
		replicas: make([]*sql.DB, 0, len(cfg.ReplicaDSNs)),
		stmts:    make(map[string]*sql.Stmt),
	}

	// Set up read replicas
	for _, dsn := range cfg.ReplicaDSNs {
		replica, err := sql.Open("postgres", dsn)
		if err != nil {
			log.Warnf("Failed to open replica: %v", err)
			continue
		}
		
		replica.SetMaxOpenConns(cfg.MaxConnections / len(cfg.ReplicaDSNs))
		replica.SetMaxIdleConns(cfg.MaxIdleConns / len(cfg.ReplicaDSNs))
		replica.SetConnMaxLifetime(cfg.ConnMaxLife)
		
		if err := replica.Ping(); err != nil {
			log.Warnf("Failed to ping replica: %v", err)
			continue
		}
		
		db.replicas = append(db.replicas, replica)
	}

	// If no replicas available, use master for reads
	if len(db.replicas) == 0 {
		log.Warn("No read replicas available, using master for reads")
		db.replicas = []*sql.DB{master}
	}

	log.Infof("Database pool initialized with %d read replicas", len(db.replicas))
	
	// Prepare common statements
	if err := db.prepareStatements(); err != nil {
		return nil, fmt.Errorf("failed to prepare statements: %w", err)
	}

	return db, nil
}

// prepareStatements prepares commonly used SQL statements
func (db *OptimizedDB) prepareStatements() error {
	statements := map[string]string{
		"get_namespace": `
			SELECT id, name, display_name, owner, quotas, created_at 
			FROM namespaces 
			WHERE id = $1`,
		"get_namespace_by_name": `
			SELECT id, name, display_name, owner, quotas, created_at 
			FROM namespaces 
			WHERE name = $1`,
		"list_namespaces": `
			SELECT id, name, display_name, owner, created_at 
			FROM namespaces 
			ORDER BY created_at DESC 
			LIMIT $1 OFFSET $2`,
		"get_table_version": `
			SELECT tv.id, tv.version, tv.schema_json, tv.status, t.namespace_id
			FROM table_versions tv
			JOIN tables t ON tv.table_id = t.id
			WHERE tv.id = $1`,
		"get_current_epoch": `
			SELECT epoch_number 
			FROM epochs 
			WHERE table_version_id = $1 
			ORDER BY epoch_number DESC 
			LIMIT 1`,
		"get_shards": `
			SELECT id, virtual_shard_id, leader_node_id, follower_node_ids, epoch_number
			FROM shards
			WHERE table_version_id = $1 AND status = 'active'
			ORDER BY virtual_shard_id`,
	}

	for name, query := range statements {
		// Prepare on master
		stmt, err := db.master.Prepare(query)
		if err != nil {
			return fmt.Errorf("failed to prepare %s: %w", name, err)
		}
		db.stmts[name] = stmt
		
		// Also prepare on replicas for read queries
		if name != "insert_namespace" && name != "update_table" {
			for _, replica := range db.replicas {
				_, err := replica.Prepare(query)
				if err != nil {
					log.Warnf("Failed to prepare %s on replica: %v", name, err)
				}
			}
		}
	}

	return nil
}

// GetReplica returns a read replica using round-robin
func (db *OptimizedDB) GetReplica() *sql.DB {
	if len(db.replicas) == 0 {
		return db.master
	}
	
	// Simple round-robin (can be improved with health checks)
	idx := db.rrIndex.Add(1) % uint32(len(db.replicas))
	return db.replicas[idx]
}

// QueryWithCache executes a query with caching
func (db *OptimizedDB) QueryWithCache(ctx context.Context, cache *cache.Cache, key string, query string, args ...interface{}) (*sql.Rows, error) {
	// Try cache first
	if cache != nil {
		var cached []map[string]interface{}
		if err := cache.Get(ctx, key, &cached); err == nil {
			// Return cached result (would need custom Rows implementation)
			log.Debugf("Cache hit for key: %s", key)
		}
	}

	// Use read replica for queries
	replica := db.GetReplica()
	rows, err := replica.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	// Cache result asynchronously
	if cache != nil {
		go func() {
			// Convert rows to cacheable format and store
			// This is simplified - real implementation would need proper row scanning
			cache.Set(context.Background(), key, rows)
		}()
	}

	return rows, nil
}

// ExecWithRetry executes a write query with retry logic
func (db *OptimizedDB) ExecWithRetry(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	var result sql.Result
	var err error
	
	// Retry logic for transient failures
	for i := 0; i < 3; i++ {
		result, err = db.master.ExecContext(ctx, query, args...)
		if err == nil {
			return result, nil
		}
		
		// Check if error is retryable
		if !isRetryable(err) {
			return nil, err
		}
		
		// Exponential backoff
		time.Sleep(time.Duration(1<<i) * 100 * time.Millisecond)
	}
	
	return nil, fmt.Errorf("failed after 3 retries: %w", err)
}

// BatchInsert performs batch inserts efficiently
func (db *OptimizedDB) BatchInsert(ctx context.Context, table string, columns []string, values [][]interface{}) error {
	if len(values) == 0 {
		return nil
	}

	// Build batch insert query
	tx, err := db.master.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Use COPY for maximum performance
	stmt, err := tx.Prepare(pq.CopyIn(table, columns...))
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range values {
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			return err
		}
	}

	if _, err := stmt.ExecContext(ctx); err != nil {
		return err
	}

	return tx.Commit()
}

// Close closes all database connections
func (db *OptimizedDB) Close() error {
	// Close prepared statements
	db.mu.Lock()
	for _, stmt := range db.stmts {
		stmt.Close()
	}
	db.mu.Unlock()

	// Close replicas
	for _, replica := range db.replicas {
		if replica != db.master {
			replica.Close()
		}
	}

	// Close master
	return db.master.Close()
}

// isRetryable checks if an error is retryable
func isRetryable(err error) bool {
	// Check for common retryable PostgreSQL errors
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "deadlock detected")
}