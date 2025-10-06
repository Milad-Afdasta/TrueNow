package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"sync/atomic"
)

// QueryCache caches query results in Redis
type QueryCache struct {
	client  *redis.Client
	enabled bool
	ttl     time.Duration
	ctx     context.Context
	hits    atomic.Uint64
	misses  atomic.Uint64
}

// NewQueryCache creates a new query cache
func NewQueryCache(addr string, enabled bool) *QueryCache {
	if !enabled {
		log.Info("Query cache disabled")
		return &QueryCache{enabled: false}
	}

	client := redis.NewClient(&redis.Options{
		Addr:            addr,
		Password:        "",
		DB:              0,
		MaxRetries:      3,
		PoolSize:        10,
		MinIdleConns:    5,
		MaxIdleConns:    10,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	ctx := context.Background()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		log.Errorf("Failed to connect to Redis: %v", err)
		return &QueryCache{enabled: false}
	}

	log.Infof("Query cache connected to Redis at %s", addr)

	return &QueryCache{
		client:  client,
		enabled: true,
		ttl:     60 * time.Second, // Default TTL
		ctx:     ctx,
	}
}

// Get retrieves cached result
func (qc *QueryCache) Get(ctx context.Context, key string, dest interface{}) (bool, error) {
	if !qc.enabled || qc.client == nil {
		return false, nil
	}

	if ctx == nil {
		ctx = qc.ctx
	}

	val, err := qc.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		qc.misses.Add(1)
		return false, nil
	}
	if err != nil {
		qc.misses.Add(1)
		log.WithError(err).Warn("query cache: get failed")
		return false, err
	}

	if dest != nil {
		if err := json.Unmarshal(val, dest); err != nil {
			qc.misses.Add(1)
			log.WithError(err).Warn("query cache: unmarshal failed")
			return false, err
		}
	}

	qc.hits.Add(1)
	log.Debugf("Cache hit for key: %s", key)
	return true, nil
}

// Set stores result in cache
func (qc *QueryCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if !qc.enabled || qc.client == nil {
		return nil
	}

	if ttl <= 0 {
		ttl = qc.ttl
	}
	if ctx == nil {
		ctx = qc.ctx
	}

	data, err := json.Marshal(value)
	if err != nil {
		log.WithError(err).Warn("query cache: marshal failed")
		return err
	}

	if err := qc.client.Set(ctx, key, data, ttl).Err(); err != nil {
		log.WithError(err).Warn("query cache: set failed")
		return err
	}

	log.Debugf("Cached result with key: %s, ttl: %v", key, ttl)
	return nil
}

// Delete removes cached result
func (qc *QueryCache) Delete(ctx context.Context, key string) {
	if !qc.enabled || qc.client == nil {
		return
	}

	if ctx == nil {
		ctx = qc.ctx
	}

	if err := qc.client.Del(ctx, key).Err(); err != nil {
		log.WithError(err).Warn("query cache: delete failed")
	}
}

// Clear clears all cached queries
func (qc *QueryCache) Clear(ctx context.Context) error {
	if !qc.enabled || qc.client == nil {
		return nil
	}

	if ctx == nil {
		ctx = qc.ctx
	}

	iter := qc.client.Scan(ctx, 0, "query:*", 0).Iterator()
	var keys []string

	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return err
	}

	if len(keys) > 0 {
		if err := qc.client.Del(ctx, keys...).Err(); err != nil {
			return err
		}
		log.Infof("Cleared %d cached queries", len(keys))
	}

	return nil
}

// Close closes Redis connection
func (qc *QueryCache) Close() {
	if qc.client != nil {
		qc.client.Close()
		log.Info("Query cache closed")
	}
}

// GetStats returns cache statistics
func (qc *QueryCache) GetStats() map[string]interface{} {
	if !qc.enabled || qc.client == nil {
		return map[string]interface{}{"enabled": false}
	}

	return map[string]interface{}{
		"enabled": true,
		"hits":    qc.hits.Load(),
		"misses":  qc.misses.Load(),
	}
}
