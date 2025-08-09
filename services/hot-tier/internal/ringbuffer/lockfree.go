package ringbuffer

import (
	"runtime"
	"sync/atomic"
	"unsafe"
)

// CacheLine size to avoid false sharing
const CacheLineSize = 64

// RingBuffer is a lock-free, cache-aligned ring buffer for time-series data
// Optimized for single-writer, multiple-reader scenarios (SWMR)
type RingBuffer struct {
	// Cache line 1: Write-side data
	_          [CacheLineSize]byte // Padding
	head       atomic.Uint64       // Write position
	_          [CacheLineSize - 8]byte
	
	// Cache line 2: Read-side data  
	tail       atomic.Uint64       // Read position
	_          [CacheLineSize - 8]byte
	
	// Cache line 3: Shared immutable data
	buffer     []unsafe.Pointer    // Actual buffer
	capacity   uint64             // Power of 2 for fast modulo
	mask       uint64             // capacity - 1 for bitwise AND
	slotSize   int                // Size of each slot
	_          [CacheLineSize - 32]byte
	
	// Stats (separate cache line)
	writes     atomic.Uint64
	reads      atomic.Uint64
	overwrites atomic.Uint64
	_          [CacheLineSize - 24]byte
}

// Slot represents a time bucket in the ring buffer
type Slot struct {
	// Aligned to cache line for optimal performance
	_         [CacheLineSize]byte
	Timestamp int64                      // Unix timestamp in milliseconds
	Groups    map[string]*AggregateGroup // Group aggregates
	Version   atomic.Uint64              // Version for lock-free updates
	_         [CacheLineSize - 24]byte
}

// AggregateGroup holds aggregated metrics for a group
type AggregateGroup struct {
	// Atomic fields for lock-free updates
	Sum        atomic.Uint64  // Sum of values (as uint64 bits)
	Count      atomic.Uint64  // Count of values
	Min        atomic.Uint64  // Minimum value (as uint64 bits)
	Max        atomic.Uint64  // Maximum value (as uint64 bits)
	
	// Sketch data (not atomic, needs synchronization)
	HLL        *HyperLogLog   // For unique counts
	TDigest    *TDigest       // For percentiles
	TopK       *SpaceSaving   // For top-K
}

// NewRingBuffer creates a new lock-free ring buffer
func NewRingBuffer(capacity uint64, slotSize int) *RingBuffer {
	// Ensure capacity is power of 2
	if capacity&(capacity-1) != 0 {
		// Round up to next power of 2
		v := capacity
		v--
		v |= v >> 1
		v |= v >> 2
		v |= v >> 4
		v |= v >> 8
		v |= v >> 16
		v |= v >> 32
		v++
		capacity = v
	}
	
	rb := &RingBuffer{
		buffer:   make([]unsafe.Pointer, capacity),
		capacity: capacity,
		mask:     capacity - 1,
		slotSize: slotSize,
	}
	
	// Pre-allocate all slots
	for i := uint64(0); i < capacity; i++ {
		slot := &Slot{
			Groups: make(map[string]*AggregateGroup),
		}
		rb.buffer[i] = unsafe.Pointer(slot)
	}
	
	return rb
}

// Write adds data to the ring buffer (single writer)
func (rb *RingBuffer) Write(timestamp int64, groupKey string, value float64) {
	// Get write position
	pos := rb.head.Add(1) - 1
	idx := pos & rb.mask
	
	// Get slot
	slotPtr := rb.buffer[idx]
	slot := (*Slot)(slotPtr)
	
	// Check if we need to clear old data (overwriting)
	if pos >= rb.capacity {
		oldTail := rb.tail.Load()
		if pos-oldTail >= rb.capacity {
			// Buffer is full, move tail forward
			rb.tail.CompareAndSwap(oldTail, pos-rb.capacity+1)
			rb.overwrites.Add(1)
			
			// Clear old slot data
			slot.Groups = make(map[string]*AggregateGroup)
		}
	}
	
	// Update slot timestamp
	slot.Timestamp = timestamp
	
	// Get or create group
	group, exists := slot.Groups[groupKey]
	if !exists {
		group = &AggregateGroup{
			HLL:     NewHyperLogLog(14), // 16KB for 0.8% error
			TDigest: NewTDigest(100),    // Compression 100
			TopK:    NewSpaceSaving(100), // Top 100
		}
		slot.Groups[groupKey] = group
		
		// Initialize atomic values
		group.Sum.Store(float64ToUint64(value))
		group.Count.Store(1)
		group.Min.Store(float64ToUint64(value))
		group.Max.Store(float64ToUint64(value))
	} else {
		// Update aggregates atomically
		rb.updateAggregates(group, value)
	}
	
	// Increment version
	slot.Version.Add(1)
	
	// Update stats
	rb.writes.Add(1)
}

// BatchWrite writes multiple values efficiently
func (rb *RingBuffer) BatchWrite(data []TimeSeriesPoint) {
	// Group by timestamp for efficient batching
	batches := make(map[int64][]TimeSeriesPoint)
	for _, point := range data {
		batches[point.Timestamp] = append(batches[point.Timestamp], point)
	}
	
	// Process each timestamp batch
	for timestamp, points := range batches {
		// Get slot for this timestamp
		pos := rb.head.Add(1) - 1
		idx := pos & rb.mask
		slotPtr := rb.buffer[idx]
		slot := (*Slot)(slotPtr)
		
		slot.Timestamp = timestamp
		
		// Process all points for this timestamp
		for _, point := range points {
			group, exists := slot.Groups[point.GroupKey]
			if !exists {
				group = &AggregateGroup{
					HLL:     NewHyperLogLog(14),
					TDigest: NewTDigest(100),
					TopK:    NewSpaceSaving(100),
				}
				slot.Groups[point.GroupKey] = group
			}
			
			rb.updateAggregates(group, point.Value)
		}
		
		slot.Version.Add(1)
	}
	
	rb.writes.Add(uint64(len(data)))
}

// GetHead returns the current head position
func (rb *RingBuffer) GetHead() uint64 {
	return rb.head.Load()
}

// GetTail returns the current tail position
func (rb *RingBuffer) GetTail() uint64 {
	return rb.tail.Load()
}

// Read reads data from a specific position (wait-free for readers)
func (rb *RingBuffer) Read(position uint64) (*Slot, bool) {
	head := rb.head.Load()
	tail := rb.tail.Load()
	
	// Check bounds
	if position < tail || position >= head {
		return nil, false
	}
	
	idx := position & rb.mask
	slotPtr := rb.buffer[idx]
	slot := (*Slot)(slotPtr)
	
	rb.reads.Add(1)
	
	// Return a copy to avoid race conditions
	return rb.copySlot(slot), true
}

// ReadRange reads a range of slots
func (rb *RingBuffer) ReadRange(startPos, endPos uint64) []*Slot {
	head := rb.head.Load()
	tail := rb.tail.Load()
	
	// Adjust bounds
	if startPos < tail {
		startPos = tail
	}
	if endPos > head {
		endPos = head
	}
	
	if startPos >= endPos {
		return nil
	}
	
	results := make([]*Slot, 0, endPos-startPos)
	
	for pos := startPos; pos < endPos; pos++ {
		idx := pos & rb.mask
		slotPtr := rb.buffer[idx]
		slot := (*Slot)(slotPtr)
		results = append(results, rb.copySlot(slot))
	}
	
	rb.reads.Add(uint64(len(results)))
	
	return results
}

// updateAggregates updates group aggregates atomically
func (rb *RingBuffer) updateAggregates(group *AggregateGroup, value float64) {
	valueUint := float64ToUint64(value)
	
	// Update sum
	for {
		oldSum := group.Sum.Load()
		newSum := float64ToUint64(uint64ToFloat64(oldSum) + value)
		if group.Sum.CompareAndSwap(oldSum, newSum) {
			break
		}
		runtime.Gosched()
	}
	
	// Update count
	group.Count.Add(1)
	
	// Update min
	for {
		oldMin := group.Min.Load()
		if uint64ToFloat64(valueUint) >= uint64ToFloat64(oldMin) {
			break
		}
		if group.Min.CompareAndSwap(oldMin, valueUint) {
			break
		}
		runtime.Gosched()
	}
	
	// Update max
	for {
		oldMax := group.Max.Load()
		if uint64ToFloat64(valueUint) <= uint64ToFloat64(oldMax) {
			break
		}
		if group.Max.CompareAndSwap(oldMax, valueUint) {
			break
		}
		runtime.Gosched()
	}
}

// copySlot creates a snapshot copy of a slot
func (rb *RingBuffer) copySlot(slot *Slot) *Slot {
	copy := &Slot{
		Timestamp: slot.Timestamp,
		Groups:    make(map[string]*AggregateGroup),
	}
	
	// Deep copy groups
	for key, group := range slot.Groups {
		copyGroup := &AggregateGroup{}
		copyGroup.Sum.Store(group.Sum.Load())
		copyGroup.Count.Store(group.Count.Load())
		copyGroup.Min.Store(group.Min.Load())
		copyGroup.Max.Store(group.Max.Load())
		
		// Copy sketches (these need proper copying in production)
		if group.HLL != nil {
			copyGroup.HLL = group.HLL.Clone()
		}
		if group.TDigest != nil {
			copyGroup.TDigest = group.TDigest.Clone()
		}
		if group.TopK != nil {
			copyGroup.TopK = group.TopK.Clone()
		}
		
		copy.Groups[key] = copyGroup
	}
	
	copy.Version.Store(slot.Version.Load())
	
	return copy
}

// GetStats returns buffer statistics
func (rb *RingBuffer) GetStats() map[string]uint64 {
	return map[string]uint64{
		"writes":     rb.writes.Load(),
		"reads":      rb.reads.Load(),
		"overwrites": rb.overwrites.Load(),
		"head":       rb.head.Load(),
		"tail":       rb.tail.Load(),
		"capacity":   rb.capacity,
	}
}

// TimeSeriesPoint represents a single data point
type TimeSeriesPoint struct {
	Timestamp int64
	GroupKey  string
	Value     float64
	UniqueID  string // For unique counting
}

// Helper functions for float64 <-> uint64 conversion (for atomic operations)
func float64ToUint64(f float64) uint64 {
	return *(*uint64)(unsafe.Pointer(&f))
}

func uint64ToFloat64(u uint64) float64 {
	return *(*float64)(unsafe.Pointer(&u))
}