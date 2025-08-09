package ringbuffer

import (
	"container/heap"
	"sort"
	"sync"
)

// SpaceSaving implements the Space-Saving algorithm for finding top-K frequent items
// with bounded memory usage
type SpaceSaving struct {
	k         int
	counters  map[string]*Counter
	minHeap   *CounterHeap
	mu        sync.RWMutex
}

// Counter represents an item with its count and error
type Counter struct {
	Item  string
	Count int64
	Error int64
	Index int // Index in heap
}

// NewSpaceSaving creates a new Space-Saving data structure
func NewSpaceSaving(k int) *SpaceSaving {
	if k < 1 {
		k = 1
	}
	
	ss := &SpaceSaving{
		k:        k,
		counters: make(map[string]*Counter),
		minHeap:  &CounterHeap{},
	}
	
	heap.Init(ss.minHeap)
	
	return ss
}

// Add increments the count for an item
func (ss *SpaceSaving) Add(item string, count int64) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	if counter, exists := ss.counters[item]; exists {
		// Item exists, increment its count
		counter.Count += count
		heap.Fix(ss.minHeap, counter.Index)
	} else if len(ss.counters) < ss.k {
		// Space available, add new counter
		counter := &Counter{
			Item:  item,
			Count: count,
			Error: 0,
		}
		ss.counters[item] = counter
		heap.Push(ss.minHeap, counter)
	} else {
		// No space, replace minimum counter
		minCounter := (*ss.minHeap)[0]
		
		// Remove old item from map
		delete(ss.counters, minCounter.Item)
		
		// Update counter with new item
		minCounter.Item = item
		minCounter.Error = minCounter.Count
		minCounter.Count += count
		
		// Add to map and fix heap
		ss.counters[item] = minCounter
		heap.Fix(ss.minHeap, 0)
	}
}

// TopK returns the top-K items with their counts
func (ss *SpaceSaving) TopK() []ItemCount {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	
	// Create slice of all counters
	items := make([]ItemCount, 0, len(ss.counters))
	for _, counter := range ss.counters {
		items = append(items, ItemCount{
			Item:  counter.Item,
			Count: counter.Count,
			Error: counter.Error,
		})
	}
	
	// Sort by count (descending)
	sort.Slice(items, func(i, j int) bool {
		return items[i].Count > items[j].Count
	})
	
	// Return top-K
	if len(items) > ss.k {
		items = items[:ss.k]
	}
	
	return items
}

// Estimate returns the estimated count for an item
func (ss *SpaceSaving) Estimate(item string) (count int64, exists bool) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	
	if counter, ok := ss.counters[item]; ok {
		return counter.Count, true
	}
	
	return 0, false
}

// Merge combines another SpaceSaving into this one
func (ss *SpaceSaving) Merge(other *SpaceSaving) {
	other.mu.RLock()
	otherItems := other.TopK()
	other.mu.RUnlock()
	
	for _, item := range otherItems {
		ss.Add(item.Item, item.Count)
	}
}

// Clone creates a copy of the SpaceSaving structure
func (ss *SpaceSaving) Clone() *SpaceSaving {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	
	clone := NewSpaceSaving(ss.k)
	
	for _, counter := range ss.counters {
		cloneCounter := &Counter{
			Item:  counter.Item,
			Count: counter.Count,
			Error: counter.Error,
		}
		clone.counters[counter.Item] = cloneCounter
		heap.Push(clone.minHeap, cloneCounter)
	}
	
	return clone
}

// Clear resets the Space-Saving structure
func (ss *SpaceSaving) Clear() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	ss.counters = make(map[string]*Counter)
	ss.minHeap = &CounterHeap{}
	heap.Init(ss.minHeap)
}

// Size returns the current number of tracked items
func (ss *SpaceSaving) Size() int {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return len(ss.counters)
}

// ItemCount represents an item with its count and error bound
type ItemCount struct {
	Item  string
	Count int64
	Error int64
}

// CounterHeap is a min-heap of counters
type CounterHeap []*Counter

func (h CounterHeap) Len() int { return len(h) }

func (h CounterHeap) Less(i, j int) bool {
	return h[i].Count < h[j].Count
}

func (h CounterHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].Index = i
	h[j].Index = j
}

func (h *CounterHeap) Push(x interface{}) {
	n := len(*h)
	counter := x.(*Counter)
	counter.Index = n
	*h = append(*h, counter)
}

func (h *CounterHeap) Pop() interface{} {
	old := *h
	n := len(old)
	counter := old[n-1]
	old[n-1] = nil
	counter.Index = -1
	*h = old[0 : n-1]
	return counter
}