package hasher

import (
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/pkg/types"
)

// ShardHasher handles key to shard mapping with hot key mitigation
type ShardHasher struct {
	mu sync.RWMutex
	
	// Configuration
	config           *types.HashingConfig
	virtualShardCount int
	physicalShardCount int
	
	// Virtual to physical shard mapping
	virtualToPhysical []int32 // Index = virtual shard, Value = physical shard
	
	// Salted keys for hot key mitigation
	saltedKeys map[string]string // Key -> Salt
	keySalts   sync.Map         // Thread-safe key->salt mapping
	
	// Consistent hash ring (if enabled)
	hashRing   *ConsistentHashRing
	
	// Statistics
	hashCount  atomic.Uint64
	saltCount  atomic.Uint64
}

// ConsistentHashRing implements consistent hashing with virtual nodes
type ConsistentHashRing struct {
	mu          sync.RWMutex
	nodes       map[uint32]int32  // Hash -> Physical Shard
	sortedKeys  []uint32
	virtualNodes int
}

// NewShardHasher creates a new shard hasher
func NewShardHasher(config *types.HashingConfig, physicalShards int) *ShardHasher {
	h := &ShardHasher{
		config:             config,
		virtualShardCount:  config.VirtualShardCount,
		physicalShardCount: physicalShards,
		virtualToPhysical:  make([]int32, config.VirtualShardCount),
		saltedKeys:         make(map[string]string),
	}
	
	// Initialize virtual to physical mapping with round-robin
	for i := 0; i < config.VirtualShardCount; i++ {
		h.virtualToPhysical[i] = int32(i % physicalShards)
	}
	
	// Initialize consistent hash ring if enabled
	if config.ConsistentHash {
		h.hashRing = NewConsistentHashRing(config.VirtualShardCount)
		for i := 0; i < physicalShards; i++ {
			h.hashRing.AddNode(int32(i))
		}
	}
	
	return h
}

// GetShard returns the shard for a given key
func (h *ShardHasher) GetShard(key string) int32 {
	h.hashCount.Add(1)
	
	// Check if key is salted (hot key mitigation)
	if salt, ok := h.keySalts.Load(key); ok {
		saltedKey := fmt.Sprintf("%s%s%s", h.config.SaltPrefix, key, salt)
		return h.computeShard(saltedKey)
	}
	
	return h.computeShard(key)
}

// GetShardWithPowerOfTwo returns shard using power of two choices for better load distribution
func (h *ShardHasher) GetShardWithPowerOfTwo(key string, loadFunc func(int32) float64) int32 {
	if !h.config.PowerOfTwoChoices {
		return h.GetShard(key)
	}
	
	// Get two candidate shards
	shard1 := h.computeShard(key)
	shard2 := h.computeShard(key + "_alt")
	
	// Choose the less loaded one
	if loadFunc(shard1) <= loadFunc(shard2) {
		return shard1
	}
	return shard2
}

// computeShard computes the shard for a key
func (h *ShardHasher) computeShard(key string) int32 {
	if h.config.ConsistentHash && h.hashRing != nil {
		return h.hashRing.GetNode(key)
	}
	
	// Use FNV-1a hash by default (fast and good distribution)
	hash := h.hashKey(key)
	
	// Map to virtual shard first
	virtualShard := int32(hash % uint64(h.virtualShardCount))
	
	// Map virtual shard to physical shard
	h.mu.RLock()
	physicalShard := h.virtualToPhysical[virtualShard]
	h.mu.RUnlock()
	
	return physicalShard
}

// hashKey hashes a key using the configured algorithm
func (h *ShardHasher) hashKey(key string) uint64 {
	switch h.config.Algorithm {
	case "fnv1a":
		return h.fnv1aHash(key)
	case "xxhash":
		// Would use xxhash library in production
		return h.fnv1aHash(key) // Fallback for now
	case "murmur3":
		// Would use murmur3 library in production
		return h.fnv1aHash(key) // Fallback for now
	default:
		return h.fnv1aHash(key)
	}
}

// fnv1aHash computes FNV-1a hash
func (h *ShardHasher) fnv1aHash(key string) uint64 {
	hash := fnv.New64a()
	hash.Write([]byte(key))
	return hash.Sum64()
}

// AddSalt adds a salt to a hot key to distribute its load
func (h *ShardHasher) AddSalt(key string, salt string) {
	h.mu.Lock()
	h.saltedKeys[key] = salt
	h.mu.Unlock()
	
	h.keySalts.Store(key, salt)
	h.saltCount.Add(1)
}

// RemoveSalt removes salt from a key
func (h *ShardHasher) RemoveSalt(key string) {
	h.mu.Lock()
	delete(h.saltedKeys, key)
	h.mu.Unlock()
	
	h.keySalts.Delete(key)
}

// UpdateVirtualMapping updates the virtual to physical shard mapping
func (h *ShardHasher) UpdateVirtualMapping(newMapping []int32) error {
	if len(newMapping) != h.virtualShardCount {
		return fmt.Errorf("mapping size mismatch: expected %d, got %d", 
			h.virtualShardCount, len(newMapping))
	}
	
	h.mu.Lock()
	copy(h.virtualToPhysical, newMapping)
	h.mu.Unlock()
	
	return nil
}

// RebalanceVirtualShards redistributes virtual shards for better balance
func (h *ShardHasher) RebalanceVirtualShards(shardLoads map[int32]float64) []int32 {
	h.mu.RLock()
	currentMapping := make([]int32, len(h.virtualToPhysical))
	copy(currentMapping, h.virtualToPhysical)
	h.mu.RUnlock()
	
	// Calculate target virtual shards per physical shard
	totalLoad := 0.0
	for _, load := range shardLoads {
		totalLoad += load
	}
	
	if totalLoad == 0 {
		return currentMapping // No load, no rebalancing needed
	}
	
	// Calculate ideal virtual shard count per physical shard
	targetVirtualShards := make(map[int32]int)
	remainingVirtual := h.virtualShardCount
	
	type shardLoad struct {
		shardID int32
		load    float64
	}
	
	// Sort shards by load to assign virtual shards proportionally
	shards := make([]shardLoad, 0, len(shardLoads))
	for id, load := range shardLoads {
		shards = append(shards, shardLoad{id, load})
	}
	sort.Slice(shards, func(i, j int) bool {
		return shards[i].load < shards[j].load // Least loaded first
	})
	
	// Assign virtual shards inversely proportional to load
	// (give more virtual shards to less loaded physical shards)
	for _, shard := range shards {
		// Inverse load factor: less loaded shards get more virtual shards
		inverseFactor := (totalLoad - shard.load) / totalLoad
		if inverseFactor < 0.1 {
			inverseFactor = 0.1 // Minimum 10% share
		}
		
		virtualCount := int(float64(h.virtualShardCount) * inverseFactor / float64(len(shardLoads)))
		if virtualCount < 1 {
			virtualCount = 1
		}
		if virtualCount > remainingVirtual {
			virtualCount = remainingVirtual
		}
		
		targetVirtualShards[shard.shardID] = virtualCount
		remainingVirtual -= virtualCount
	}
	
	// Distribute any remaining virtual shards
	for remainingVirtual > 0 {
		for _, shard := range shards {
			if remainingVirtual > 0 {
				targetVirtualShards[shard.shardID]++
				remainingVirtual--
			}
		}
	}
	
	// Create new mapping
	newMapping := make([]int32, h.virtualShardCount)
	virtualIndex := 0
	
	for shardID, count := range targetVirtualShards {
		for i := 0; i < count && virtualIndex < h.virtualShardCount; i++ {
			newMapping[virtualIndex] = shardID
			virtualIndex++
		}
	}
	
	// Shuffle to avoid sequential assignment (better distribution)
	h.shuffleMapping(newMapping)
	
	return newMapping
}

// shuffleMapping shuffles virtual shard mapping for better distribution
func (h *ShardHasher) shuffleMapping(mapping []int32) {
	// Use deterministic shuffle based on current mapping
	// This ensures consistency across nodes
	n := len(mapping)
	for i := n - 1; i > 0; i-- {
		j := int(h.fnv1aHash(fmt.Sprintf("%d", i))) % (i + 1)
		mapping[i], mapping[j] = mapping[j], mapping[i]
	}
}

// GetStatistics returns hasher statistics
func (h *ShardHasher) GetStatistics() map[string]interface{} {
	h.mu.RLock()
	saltedCount := len(h.saltedKeys)
	h.mu.RUnlock()
	
	return map[string]interface{}{
		"hash_count":         h.hashCount.Load(),
		"salt_count":         h.saltCount.Load(),
		"salted_keys":        saltedCount,
		"virtual_shards":     h.virtualShardCount,
		"physical_shards":    h.physicalShardCount,
		"algorithm":          h.config.Algorithm,
		"consistent_hash":    h.config.ConsistentHash,
		"power_of_two":       h.config.PowerOfTwoChoices,
	}
}

// ConsistentHashRing implementation

func NewConsistentHashRing(virtualNodes int) *ConsistentHashRing {
	return &ConsistentHashRing{
		nodes:        make(map[uint32]int32),
		sortedKeys:   make([]uint32, 0),
		virtualNodes: virtualNodes,
	}
}

func (r *ConsistentHashRing) AddNode(shardID int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Add multiple virtual nodes for better distribution
	for i := 0; i < r.virtualNodes; i++ {
		virtualKey := fmt.Sprintf("shard-%d-vnode-%d", shardID, i)
		hash := r.hashString(virtualKey)
		r.nodes[hash] = shardID
		r.sortedKeys = append(r.sortedKeys, hash)
	}
	
	// Keep sorted for binary search
	sort.Slice(r.sortedKeys, func(i, j int) bool {
		return r.sortedKeys[i] < r.sortedKeys[j]
	})
}

func (r *ConsistentHashRing) RemoveNode(shardID int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Remove all virtual nodes for this shard
	newNodes := make(map[uint32]int32)
	newKeys := make([]uint32, 0)
	
	for hash, shard := range r.nodes {
		if shard != shardID {
			newNodes[hash] = shard
			newKeys = append(newKeys, hash)
		}
	}
	
	r.nodes = newNodes
	r.sortedKeys = newKeys
	
	sort.Slice(r.sortedKeys, func(i, j int) bool {
		return r.sortedKeys[i] < r.sortedKeys[j]
	})
}

func (r *ConsistentHashRing) GetNode(key string) int32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	if len(r.sortedKeys) == 0 {
		return 0
	}
	
	hash := r.hashString(key)
	
	// Binary search for the first node with hash >= key hash
	idx := sort.Search(len(r.sortedKeys), func(i int) bool {
		return r.sortedKeys[i] >= hash
	})
	
	// Wrap around to the first node if necessary
	if idx == len(r.sortedKeys) {
		idx = 0
	}
	
	return r.nodes[r.sortedKeys[idx]]
}

func (r *ConsistentHashRing) hashString(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// JumpHash implements Google's jump consistent hash algorithm
// This provides optimal load distribution with minimal memory usage
func JumpHash(key uint64, numBuckets int32) int32 {
	var b int64 = -1
	var j int64 = 0
	
	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * float64(int64(1)<<31) / float64((key>>33)+1))
	}
	
	return int32(b)
}

// MaglevHash implements Google's Maglev consistent hashing
// This provides excellent distribution and minimal disruption during changes
type MaglevHasher struct {
	tableSize int
	lookup    []int32
	backends  []string
}

func NewMaglevHasher(backends []string, tableSize int) *MaglevHasher {
	// Table size should be prime for best distribution
	if !isPrime(tableSize) {
		tableSize = nextPrime(tableSize)
	}
	
	m := &MaglevHasher{
		tableSize: tableSize,
		lookup:    make([]int32, tableSize),
		backends:  backends,
	}
	
	m.generate()
	return m
}

func (m *MaglevHasher) generate() {
	n := len(m.backends)
	if n == 0 {
		return
	}
	
	// Generate preference lists for each backend
	offset := make([]uint64, n)
	skip := make([]uint64, n)
	
	for i, backend := range m.backends {
		h1, h2 := m.hash(backend)
		offset[i] = h1 % uint64(m.tableSize)
		skip[i] = h2%(uint64(m.tableSize)-1) + 1
	}
	
	// Build lookup table
	entry := make([]int, m.tableSize)
	for i := range entry {
		entry[i] = -1
	}
	
	next := make([]uint64, n)
	for i := range next {
		next[i] = offset[i]
	}
	
	k := 0
	for j := 0; j < m.tableSize; j++ {
		for {
			idx := next[k] % uint64(m.tableSize)
			if entry[idx] == -1 {
				entry[idx] = k
				next[k] = (next[k] + skip[k]) % uint64(m.tableSize)
				break
			}
			next[k] = (next[k] + skip[k]) % uint64(m.tableSize)
			k = (k + 1) % n
		}
		k = (k + 1) % n
	}
	
	// Convert to shard IDs
	for i, e := range entry {
		m.lookup[i] = int32(e)
	}
}

func (m *MaglevHasher) hash(s string) (uint64, uint64) {
	data := []byte(s)
	h1 := fnv.New64a()
	h1.Write(data)
	h2 := fnv.New64()
	h2.Write(data)
	return h1.Sum64(), h2.Sum64()
}

func (m *MaglevHasher) GetShard(key string) int32 {
	h, _ := m.hash(key)
	return m.lookup[h%uint64(m.tableSize)]
}

// Helper functions

func isPrime(n int) bool {
	if n <= 1 {
		return false
	}
	if n <= 3 {
		return true
	}
	if n%2 == 0 || n%3 == 0 {
		return false
	}
	for i := 5; i*i <= n; i += 6 {
		if n%i == 0 || n%(i+2) == 0 {
			return false
		}
	}
	return true
}

func nextPrime(n int) int {
	for !isPrime(n) {
		n++
	}
	return n
}