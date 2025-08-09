package ratelimit

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

const numShards = 32 // Number of shards for the tenant map

// HierarchicalLimiter implements hierarchical token bucket rate limiting
// Optimized for high-throughput with lock-free operations where possible
type HierarchicalLimiter struct {
	globalBucket  *TokenBucket
	tenantShards  [numShards]*tenantShard
	
	// Global rate (QPS)
	globalRate int64
}

// tenantShard represents a single shard of the tenant map
type tenantShard struct {
	mu      sync.RWMutex
	buckets map[string]*TokenBucket
}

// TokenBucket is a lock-free token bucket implementation
type TokenBucket struct {
	tokens    atomic.Int64
	maxTokens int64
	refillRate int64
	lastRefill atomic.Int64
}

// NewHierarchicalLimiter creates a new hierarchical rate limiter
func NewHierarchicalLimiter(globalQPS int64) *HierarchicalLimiter {
	hl := &HierarchicalLimiter{
		globalRate: globalQPS,
		globalBucket: &TokenBucket{
			maxTokens:  globalQPS,
			refillRate: globalQPS,
		},
	}
	
	// Initialize shards
	for i := 0; i < numShards; i++ {
		hl.tenantShards[i] = &tenantShard{
			buckets: make(map[string]*TokenBucket),
		}
	}
	
	hl.globalBucket.tokens.Store(globalQPS)
	hl.globalBucket.lastRefill.Store(time.Now().UnixNano())
	
	// Start refill goroutine
	go hl.refillLoop()
	
	return hl
}

// Allow checks if request is allowed under rate limits
func (hl *HierarchicalLimiter) Allow(tenant string) bool {
	// Check tenant limit first (cheaper)
	tenantBucket := hl.getTenantBucket(tenant)
	if !tenantBucket.TryConsume(1) {
		return false
	}
	
	// Then check global limit
	if !hl.globalBucket.TryConsume(1) {
		// Return token to tenant bucket since global failed
		tenantBucket.Return(1)
		return false
	}
	
	return true
}

// AllowN checks if N requests are allowed
func (hl *HierarchicalLimiter) AllowN(tenant string, n int64) bool {
	tenantBucket := hl.getTenantBucket(tenant)
	if !tenantBucket.TryConsume(n) {
		return false
	}
	
	if !hl.globalBucket.TryConsume(n) {
		tenantBucket.Return(n)
		return false
	}
	
	return true
}

// getShardIndex returns the shard index for a given tenant
func (hl *HierarchicalLimiter) getShardIndex(tenant string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(tenant))
	return h.Sum32() % numShards
}

// getTenantBucket gets or creates a tenant bucket
func (hl *HierarchicalLimiter) getTenantBucket(tenant string) *TokenBucket {
	shardIdx := hl.getShardIndex(tenant)
	shard := hl.tenantShards[shardIdx]
	
	// Try read lock first
	shard.mu.RLock()
	if bucket, exists := shard.buckets[tenant]; exists {
		shard.mu.RUnlock()
		return bucket
	}
	shard.mu.RUnlock()
	
	// Need to create new bucket
	shard.mu.Lock()
	defer shard.mu.Unlock()
	
	// Double check after acquiring write lock
	if bucket, exists := shard.buckets[tenant]; exists {
		return bucket
	}
	
	// Create new bucket with 10% of global rate as default
	tenantRate := hl.globalRate / 10
	if tenantRate < 1000 {
		tenantRate = 1000 // Min 1K QPS per tenant
	}
	
	bucket := &TokenBucket{
		maxTokens:  tenantRate,
		refillRate: tenantRate,
	}
	bucket.tokens.Store(tenantRate)
	bucket.lastRefill.Store(time.Now().UnixNano())
	
	shard.buckets[tenant] = bucket
	return bucket
}

// TryConsume attempts to consume n tokens using lock-free CAS
func (tb *TokenBucket) TryConsume(n int64) bool {
	// Fast path - try to consume without refill
	for {
		current := tb.tokens.Load()
		if current < n {
			// Try refill
			tb.refill()
			current = tb.tokens.Load()
			if current < n {
				return false
			}
		}
		
		if tb.tokens.CompareAndSwap(current, current-n) {
			return true
		}
		// CAS failed, retry
	}
}

// Return returns tokens to the bucket
func (tb *TokenBucket) Return(n int64) {
	for {
		current := tb.tokens.Load()
		newVal := current + n
		if newVal > tb.maxTokens {
			newVal = tb.maxTokens
		}
		
		if tb.tokens.CompareAndSwap(current, newVal) {
			return
		}
	}
}

// refill adds tokens based on time elapsed
func (tb *TokenBucket) refill() {
	now := time.Now().UnixNano()
	lastRefill := tb.lastRefill.Load()
	
	elapsed := now - lastRefill
	if elapsed <= 0 {
		return
	}
	
	// Try to update last refill time
	if !tb.lastRefill.CompareAndSwap(lastRefill, now) {
		// Another goroutine is refilling
		return
	}
	
	// Calculate tokens to add
	tokensToAdd := (elapsed * tb.refillRate) / int64(time.Second)
	if tokensToAdd <= 0 {
		return
	}
	
	// Add tokens with CAS
	for {
		current := tb.tokens.Load()
		newVal := current + tokensToAdd
		if newVal > tb.maxTokens {
			newVal = tb.maxTokens
		}
		
		if tb.tokens.CompareAndSwap(current, newVal) {
			return
		}
	}
}

// refillLoop periodically refills all buckets
func (hl *HierarchicalLimiter) refillLoop() {
	ticker := time.NewTicker(10 * time.Millisecond) // 100Hz refill
	defer ticker.Stop()
	
	for range ticker.C {
		// Refill global bucket
		hl.globalBucket.refill()
		
		// Refill tenant buckets in all shards
		for i := 0; i < numShards; i++ {
			shard := hl.tenantShards[i]
			shard.mu.RLock()
			for _, bucket := range shard.buckets {
				bucket.refill()
			}
			shard.mu.RUnlock()
		}
	}
}

// SetTenantLimit sets a specific limit for a tenant
func (hl *HierarchicalLimiter) SetTenantLimit(tenant string, qps int64) {
	bucket := hl.getTenantBucket(tenant)
	bucket.maxTokens = qps
	bucket.refillRate = qps
	
	// Adjust current tokens if over new limit
	for {
		current := bucket.tokens.Load()
		if current <= qps {
			break
		}
		bucket.tokens.CompareAndSwap(current, qps)
	}
}

// AllowWithLimit checks if request is allowed with custom limit
func (hl *HierarchicalLimiter) AllowWithLimit(tenant string, limit int64) bool {
	// Temporarily set tenant limit
	bucket := hl.getTenantBucket(tenant)
	oldMax := bucket.maxTokens
	oldRate := bucket.refillRate
	
	// Apply adaptive limit
	bucket.maxTokens = limit
	bucket.refillRate = limit
	
	// Try to consume
	allowed := hl.Allow(tenant)
	
	// Restore original limits
	bucket.maxTokens = oldMax
	bucket.refillRate = oldRate
	
	return allowed
}