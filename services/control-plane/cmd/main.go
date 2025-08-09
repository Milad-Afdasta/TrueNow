package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/audit"
	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Config struct {
	Server struct {
		HTTPPort string `mapstructure:"http_port"`
		GRPCPort string `mapstructure:"grpc_port"`
	} `mapstructure:"server"`
	Database struct {
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		Name     string `mapstructure:"name"`
		User     string `mapstructure:"user"`
		Password string `mapstructure:"password"`
	} `mapstructure:"database"`
}

type Server struct {
	db      *sql.DB
	config  *Config
	router  *mux.Router
	auditor *audit.Auditor
}

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	config := loadConfig()
	
	db, err := connectDB(config)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	auditor := audit.NewAuditor(db)
	
	server := &Server{
		db:      db,
		config:  config,
		router:  mux.NewRouter(),
		auditor: auditor,
	}

	server.setupRoutes()

	httpServer := &http.Server{
		Addr:    ":" + config.Server.HTTPPort,
		Handler: server.router,
	}

	go func() {
		log.Infof("Control Plane starting on port %s", config.Server.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Info("Server exited")
}

func loadConfig() *Config {
	viper.SetDefault("server.http_port", "8001")
	viper.SetDefault("server.grpc_port", "9001")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.name", "analytics")
	viper.SetDefault("database.user", os.Getenv("USER"))
	viper.SetDefault("database.password", "")

	viper.SetEnvPrefix("CONTROL")
	viper.AutomaticEnv()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Fatalf("Error reading config file: %v", err)
		}
		log.Info("No config file found, using defaults")
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Failed to unmarshal config: %v", err)
	}

	return &config
}

func connectDB(config *Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable",
		config.Database.Host,
		config.Database.Port,
		config.Database.User,
		config.Database.Name,
	)
	
	if config.Database.Password != "" {
		dsn += fmt.Sprintf(" password=%s", config.Database.Password)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	log.Info("Connected to PostgreSQL database")
	return db, nil
}

func (s *Server) setupRoutes() {
	// Health endpoints
	s.router.HandleFunc("/health", s.healthHandler).Methods("GET")
	s.router.HandleFunc("/ready", s.readyHandler).Methods("GET")
	
	// Metrics
	s.router.Handle("/metrics", promhttp.Handler())
	
	// API v1 routes
	v1 := s.router.PathPrefix("/v1").Subrouter()
	
	// Namespace endpoints
	v1.HandleFunc("/namespaces", s.createNamespace).Methods("POST")
	v1.HandleFunc("/namespaces", s.listNamespaces).Methods("GET")
	v1.HandleFunc("/namespaces/{id}", s.getNamespace).Methods("GET")
	
	// Table endpoints
	v1.HandleFunc("/namespaces/{ns}/tables", s.createTable).Methods("POST")
	v1.HandleFunc("/namespaces/{ns}/tables", s.listTables).Methods("GET")
	
	// Registry endpoints
	v1.HandleFunc("/registry/shards", s.getShardRegistry).Methods("GET")
	
	// Epoch endpoints
	v1.HandleFunc("/epochs/current", s.getCurrentEpoch).Methods("GET")
	
	// Middleware
	s.router.Use(loggingMiddleware)
	s.router.Use(s.auditor.Middleware)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.WithFields(log.Fields{
			"method":   r.Method,
			"path":     r.URL.Path,
			"duration": time.Since(start),
		}).Info("Request handled")
	})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (s *Server) createNamespace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		DisplayName string                 `json:"display_name"`
		Owner       string                 `json:"owner"`
		Quotas      map[string]interface{} `json:"quotas"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	quotasJSON, _ := json.Marshal(req.Quotas)
	
	var id, name string
	err := s.db.QueryRow(`
		INSERT INTO namespaces (name, display_name, owner, quotas) 
		VALUES ($1, $2, $3, $4)
		RETURNING id, name`,
		req.Name, req.DisplayName, req.Owner, quotasJSON,
	).Scan(&id, &name)
	
	if err != nil {
		log.Errorf("Failed to create namespace: %v", err)
		http.Error(w, "Failed to create namespace", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":   id,
		"name": name,
	})
}

func (s *Server) listNamespaces(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT id, name, display_name, owner, created_at 
		FROM namespaces 
		ORDER BY created_at DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	
	var namespaces []map[string]interface{}
	for rows.Next() {
		var id, name, displayName, owner string
		var createdAt time.Time
		
		if err := rows.Scan(&id, &name, &displayName, &owner, &createdAt); err != nil {
			continue
		}
		
		namespaces = append(namespaces, map[string]interface{}{
			"id":           id,
			"name":         name,
			"display_name": displayName,
			"owner":        owner,
			"created_at":   createdAt,
		})
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(namespaces)
}

func (s *Server) getNamespace(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	
	var name, displayName, owner string
	var quotas json.RawMessage
	var createdAt time.Time
	
	err := s.db.QueryRow(`
		SELECT name, display_name, owner, quotas, created_at 
		FROM namespaces 
		WHERE id = $1`,
		id,
	).Scan(&name, &displayName, &owner, &quotas, &createdAt)
	
	if err == sql.ErrNoRows {
		http.Error(w, "Namespace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           id,
		"name":         name,
		"display_name": displayName,
		"owner":        owner,
		"quotas":       quotas,
		"created_at":   createdAt,
	})
}

func (s *Server) createTable(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["ns"]
	
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	// First get namespace ID
	var namespaceID string
	err := s.db.QueryRow("SELECT id FROM namespaces WHERE name = $1", namespace).Scan(&namespaceID)
	if err != nil {
		http.Error(w, "Namespace not found", http.StatusNotFound)
		return
	}
	
	var id, name string
	err = s.db.QueryRow(`
		INSERT INTO tables (namespace_id, name, description) 
		VALUES ($1, $2, $3)
		RETURNING id, name`,
		namespaceID, req.Name, req.Description,
	).Scan(&id, &name)
	
	if err != nil {
		log.Errorf("Failed to create table: %v", err)
		http.Error(w, "Failed to create table", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":   id,
		"name": name,
	})
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["ns"]
	
	rows, err := s.db.Query(`
		SELECT t.id, t.name, t.active_version, t.created_at 
		FROM tables t
		JOIN namespaces n ON t.namespace_id = n.id
		WHERE n.name = $1
		ORDER BY t.created_at DESC`,
		namespace,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	
	var tables []map[string]interface{}
	for rows.Next() {
		var id, name string
		var activeVersion int
		var createdAt time.Time
		
		if err := rows.Scan(&id, &name, &activeVersion, &createdAt); err != nil {
			continue
		}
		
		tables = append(tables, map[string]interface{}{
			"id":             id,
			"name":           name,
			"active_version": activeVersion,
			"created_at":     createdAt,
		})
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}

func (s *Server) getShardRegistry(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT s.id, s.virtual_shard_id, s.leader_node_id, s.epoch_number, tv.id as table_version_id
		FROM shards s
		JOIN table_versions tv ON s.table_version_id = tv.id
		WHERE s.status = 'active'
		ORDER BY s.virtual_shard_id
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	
	var shards []map[string]interface{}
	for rows.Next() {
		var id, leaderNodeID, tableVersionID string
		var virtualShardID int
		var epochNumber int64
		
		if err := rows.Scan(&id, &virtualShardID, &leaderNodeID, &epochNumber, &tableVersionID); err != nil {
			continue
		}
		
		shards = append(shards, map[string]interface{}{
			"id":               id,
			"virtual_shard_id": virtualShardID,
			"leader_node_id":   leaderNodeID,
			"epoch_number":     epochNumber,
			"table_version_id": tableVersionID,
		})
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shards": shards,
	})
}

func (s *Server) getCurrentEpoch(w http.ResponseWriter, r *http.Request) {
	tableVersionID := r.URL.Query().Get("table_version_id")
	if tableVersionID == "" {
		http.Error(w, "table_version_id required", http.StatusBadRequest)
		return
	}
	
	var epochNumber int64
	err := s.db.QueryRow(`
		SELECT epoch_number 
		FROM epochs 
		WHERE table_version_id = $1 
		ORDER BY epoch_number DESC 
		LIMIT 1`,
		tableVersionID,
	).Scan(&epochNumber)
	
	if err == sql.ErrNoRows {
		epochNumber = 1 // Default epoch
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"epoch_number":      epochNumber,
		"table_version_id": tableVersionID,
	})
}