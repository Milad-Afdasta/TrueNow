package cache

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

// QueryCache caches query results in Redis
type QueryCache struct {
	client  *redis.Client
	enabled bool
	ttl     time.Duration
	ctx     context.Context
}

// NewQueryCache creates a new query cache
func NewQueryCache(addr string, enabled bool) *QueryCache {
	if !enabled {
		log.Info("Query cache disabled")
		return &QueryCache{enabled: false}
	}
	
	client := redis.NewClient(&redis.Options{
		Addr:           addr,
		Password:       "",
		DB:             0,
		MaxRetries:     3,
		PoolSize:       10,
		MinIdleConns:   5,
		MaxIdleConns:   10,
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
func (qc *QueryCache) Get(key string) (interface{}, bool) {
	if !qc.enabled || qc.client == nil {
		return nil, false
	}
	
	val, err := qc.client.Get(qc.ctx, key).Result()
	if err == redis.Nil {
		return nil, false
	} else if err != nil {
		log.Warnf("Cache get error: %v", err)
		return nil, false
	}
	
	var result interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		log.Warnf("Cache unmarshal error: %v", err)
		return nil, false
	}
	
	log.Debugf("Cache hit for key: %s", key)
	return result, true
}

// Set stores result in cache
func (qc *QueryCache) Set(key string, value interface{}, ttl time.Duration) {
	if !qc.enabled || qc.client == nil {
		return
	}
	
	data, err := json.Marshal(value)
	if err != nil {
		log.Warnf("Cache marshal error: %v", err)
		return
	}
	
	if ttl == 0 {
		ttl = qc.ttl
	}
	
	err = qc.client.Set(qc.ctx, key, data, ttl).Err()
	if err != nil {
		log.Warnf("Cache set error: %v", err)
		return
	}
	
	log.Debugf("Cached result with key: %s, ttl: %v", key, ttl)
}

// Delete removes cached result
func (qc *QueryCache) Delete(key string) {
	if !qc.enabled || qc.client == nil {
		return
	}
	
	err := qc.client.Del(qc.ctx, key).Err()
	if err != nil {
		log.Warnf("Cache delete error: %v", err)
	}
}

// GenerateKey generates cache key for query
func (qc *QueryCache) GenerateKey(req interface{}) string {
	// Create deterministic key from request
	data, _ := json.Marshal(req)
	h := md5.New()
	h.Write(data)
	return fmt.Sprintf("query:%s", hex.EncodeToString(h.Sum(nil)))
}

// Clear clears all cached queries
func (qc *QueryCache) Clear() error {
	if !qc.enabled || qc.client == nil {
		return nil
	}
	
	// Clear all keys with query: prefix
	iter := qc.client.Scan(qc.ctx, 0, "query:*", 0).Iterator()
	var keys []string
	
	for iter.Next(qc.ctx) {
		keys = append(keys, iter.Val())
	}
	
	if err := iter.Err(); err != nil {
		return err
	}
	
	if len(keys) > 0 {
		err := qc.client.Del(qc.ctx, keys...).Err()
		if err != nil {
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
	
	info := qc.client.Info(qc.ctx, "stats").Val()
	
	return map[string]interface{}{
		"enabled": true,
		"info":    info,
	}
}