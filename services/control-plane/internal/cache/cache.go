package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

type Cache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewCache(addr string, ttl time.Duration) *Cache {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		PoolSize:     100,           // Connection pool for high throughput
		MinIdleConns: 10,
		MaxRetries:   3,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Warnf("Redis not available, caching disabled: %v", err)
		return &Cache{client: nil, ttl: ttl}
	}

	log.Info("Connected to Redis cache")
	return &Cache{client: client, ttl: ttl}
}

func (c *Cache) Get(ctx context.Context, key string, dest interface{}) error {
	if c.client == nil {
		return fmt.Errorf("cache disabled")
	}

	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return err
	}

	return json.Unmarshal([]byte(val), dest)
}

func (c *Cache) Set(ctx context.Context, key string, value interface{}) error {
	if c.client == nil {
		return nil // Silently skip if cache is disabled
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.client.Set(ctx, key, data, c.ttl).Err()
}

func (c *Cache) Delete(ctx context.Context, patterns ...string) error {
	if c.client == nil {
		return nil
	}

	// Use pipeline for batch operations
	pipe := c.client.Pipeline()
	for _, pattern := range patterns {
		// Find all keys matching pattern
		keys, err := c.client.Keys(ctx, pattern).Result()
		if err != nil {
			continue
		}
		for _, key := range keys {
			pipe.Del(ctx, key)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (c *Cache) InvalidateNamespace(ctx context.Context, namespaceID string) error {
	patterns := []string{
		fmt.Sprintf("namespace:%s", namespaceID),
		fmt.Sprintf("tables:%s:*", namespaceID),
		fmt.Sprintf("shards:%s:*", namespaceID),
	}
	return c.Delete(ctx, patterns...)
}

// Distributed lock for preventing cache stampede
func (c *Cache) Lock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if c.client == nil {
		return true, nil // No locking if cache is disabled
	}

	return c.client.SetNX(ctx, "lock:"+key, "1", ttl).Result()
}

func (c *Cache) Unlock(ctx context.Context, key string) error {
	if c.client == nil {
		return nil
	}

	return c.client.Del(ctx, "lock:"+key).Err()
}