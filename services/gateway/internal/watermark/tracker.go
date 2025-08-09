package watermark

import (
	"sync"
	"sync/atomic"
	"time"
)

// WatermarkTracker tracks processing watermarks for data freshness
type WatermarkTracker struct {
	// Watermarks by partition
	partitionWatermarks sync.Map // map[int32]*PartitionWatermark
	
	// Global watermarks
	minWatermark atomic.Int64 // Minimum watermark across all partitions
	maxWatermark atomic.Int64 // Maximum watermark across all partitions
	
	// Processing time watermarks
	processingTime atomic.Int64 // Current processing time
	eventTime      atomic.Int64 // Latest event time seen
	
	// Lag metrics
	watermarkLag   atomic.Int64 // Difference between processing and event time
	partitionCount atomic.Int32 // Number of active partitions
	
	// Update tracking
	lastUpdate atomic.Int64 // Last watermark update time
	updateCount atomic.Int64 // Total updates
}

// PartitionWatermark tracks watermark for a single partition
type PartitionWatermark struct {
	partitionID     int32
	lowWatermark    atomic.Int64 // Oldest unprocessed event
	highWatermark   atomic.Int64 // Newest event seen
	committedOffset atomic.Int64 // Last committed offset
	pendingCount    atomic.Int64 // Events pending processing
	lastUpdate      atomic.Int64 // Last update timestamp
}

// NewWatermarkTracker creates a new watermark tracker
func NewWatermarkTracker() *WatermarkTracker {
	wt := &WatermarkTracker{}
	
	// Initialize with current time
	now := time.Now().UnixMilli()
	wt.processingTime.Store(now)
	wt.minWatermark.Store(now)
	wt.maxWatermark.Store(now)
	wt.lastUpdate.Store(now)
	
	// Start background updater
	go wt.backgroundUpdater()
	
	return wt
}

// UpdatePartitionWatermark updates watermark for a partition
func (wt *WatermarkTracker) UpdatePartitionWatermark(partitionID int32, eventTime int64, offset int64) {
	// Get or create partition watermark
	pw := wt.getOrCreatePartition(partitionID)
	
	// Update high watermark
	for {
		current := pw.highWatermark.Load()
		if eventTime <= current {
			break
		}
		if pw.highWatermark.CompareAndSwap(current, eventTime) {
			break
		}
	}
	
	// Update low watermark if this is the first event
	if pw.lowWatermark.Load() == 0 {
		pw.lowWatermark.Store(eventTime)
	}
	
	// Update pending count
	pw.pendingCount.Add(1)
	
	// Update last update time
	pw.lastUpdate.Store(time.Now().UnixMilli())
	
	// Update global watermarks
	wt.updateGlobalWatermarks(eventTime)
	
	// Track latest event time
	for {
		current := wt.eventTime.Load()
		if eventTime <= current {
			break
		}
		if wt.eventTime.CompareAndSwap(current, eventTime) {
			break
		}
	}
	
	wt.updateCount.Add(1)
	wt.lastUpdate.Store(time.Now().UnixMilli())
}

// CommitPartitionOffset marks events as processed up to offset
func (wt *WatermarkTracker) CommitPartitionOffset(partitionID int32, offset int64, count int64) {
	pw := wt.getOrCreatePartition(partitionID)
	
	// Update committed offset
	for {
		current := pw.committedOffset.Load()
		if offset <= current {
			break
		}
		if pw.committedOffset.CompareAndSwap(current, offset) {
			break
		}
	}
	
	// Decrease pending count
	pw.pendingCount.Add(-count)
	if pw.pendingCount.Load() < 0 {
		pw.pendingCount.Store(0)
	}
	
	// Move low watermark forward if all events up to a point are processed
	// This is simplified - production would track gaps
	if pw.pendingCount.Load() == 0 {
		pw.lowWatermark.Store(pw.highWatermark.Load())
	}
}

// getOrCreatePartition gets or creates a partition watermark
func (wt *WatermarkTracker) getOrCreatePartition(partitionID int32) *PartitionWatermark {
	if pw, ok := wt.partitionWatermarks.Load(partitionID); ok {
		return pw.(*PartitionWatermark)
	}
	
	pw := &PartitionWatermark{
		partitionID: partitionID,
	}
	actual, loaded := wt.partitionWatermarks.LoadOrStore(partitionID, pw)
	if !loaded {
		wt.partitionCount.Add(1)
	}
	return actual.(*PartitionWatermark)
}

// updateGlobalWatermarks updates min/max watermarks
func (wt *WatermarkTracker) updateGlobalWatermarks(eventTime int64) {
	// Update max watermark
	for {
		current := wt.maxWatermark.Load()
		if eventTime <= current {
			break
		}
		if wt.maxWatermark.CompareAndSwap(current, eventTime) {
			break
		}
	}
	
	// Calculate min watermark across all partitions
	minWatermark := int64(^uint64(0) >> 1) // MaxInt64
	wt.partitionWatermarks.Range(func(key, value interface{}) bool {
		pw := value.(*PartitionWatermark)
		low := pw.lowWatermark.Load()
		if low > 0 && low < minWatermark {
			minWatermark = low
		}
		return true
	})
	
	if minWatermark != int64(^uint64(0)>>1) {
		wt.minWatermark.Store(minWatermark)
	}
}

// backgroundUpdater periodically updates processing time and lag
func (wt *WatermarkTracker) backgroundUpdater() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for range ticker.C {
		now := time.Now().UnixMilli()
		wt.processingTime.Store(now)
		
		// Calculate watermark lag
		eventTime := wt.eventTime.Load()
		if eventTime > 0 {
			lag := now - eventTime
			wt.watermarkLag.Store(lag)
		}
		
		// Clean up stale partitions (no updates for 5 minutes)
		staleThreshold := now - 5*60*1000
		wt.partitionWatermarks.Range(func(key, value interface{}) bool {
			pw := value.(*PartitionWatermark)
			if pw.lastUpdate.Load() < staleThreshold && pw.pendingCount.Load() == 0 {
				wt.partitionWatermarks.Delete(key)
				wt.partitionCount.Add(-1)
			}
			return true
		})
	}
}

// GetWatermarks returns current watermark state
func (wt *WatermarkTracker) GetWatermarks() WatermarkState {
	state := WatermarkState{
		MinWatermark:    wt.minWatermark.Load(),
		MaxWatermark:    wt.maxWatermark.Load(),
		ProcessingTime:  wt.processingTime.Load(),
		EventTime:       wt.eventTime.Load(),
		WatermarkLag:    wt.watermarkLag.Load(),
		PartitionCount:  wt.partitionCount.Load(),
		LastUpdate:      wt.lastUpdate.Load(),
		UpdateCount:     wt.updateCount.Load(),
		PartitionStates: make(map[int32]PartitionState),
	}
	
	// Add partition states
	wt.partitionWatermarks.Range(func(key, value interface{}) bool {
		partitionID := key.(int32)
		pw := value.(*PartitionWatermark)
		state.PartitionStates[partitionID] = PartitionState{
			LowWatermark:    pw.lowWatermark.Load(),
			HighWatermark:   pw.highWatermark.Load(),
			CommittedOffset: pw.committedOffset.Load(),
			PendingCount:    pw.pendingCount.Load(),
			LastUpdate:      pw.lastUpdate.Load(),
		}
		return true
	})
	
	return state
}

// GetFreshness returns data freshness in milliseconds
func (wt *WatermarkTracker) GetFreshness() int64 {
	return wt.watermarkLag.Load()
}

// IsHealthy checks if watermarks are progressing healthily
func (wt *WatermarkTracker) IsHealthy(maxLagMs int64) bool {
	// Check if lag is within threshold
	if wt.watermarkLag.Load() > maxLagMs {
		return false
	}
	
	// Check if we've had recent updates
	lastUpdate := wt.lastUpdate.Load()
	now := time.Now().UnixMilli()
	if now-lastUpdate > 30000 { // No updates for 30 seconds
		return false
	}
	
	return true
}

// Reset resets all watermarks
func (wt *WatermarkTracker) Reset() {
	now := time.Now().UnixMilli()
	
	wt.partitionWatermarks.Range(func(key, value interface{}) bool {
		wt.partitionWatermarks.Delete(key)
		return true
	})
	
	wt.minWatermark.Store(now)
	wt.maxWatermark.Store(now)
	wt.processingTime.Store(now)
	wt.eventTime.Store(0)
	wt.watermarkLag.Store(0)
	wt.partitionCount.Store(0)
	wt.lastUpdate.Store(now)
	wt.updateCount.Store(0)
}