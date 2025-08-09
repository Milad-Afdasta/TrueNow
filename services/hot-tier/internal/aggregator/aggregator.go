package aggregator

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Milad-Afdasta/TrueNow/services/hot-tier/internal/ringbuffer"
	pb "github.com/Milad-Afdasta/TrueNow/shared/proto/pb/hottier"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Config holds aggregator configuration
type Config struct {
	ShardID        int
	NumCPU         int
	MemoryGB       uint64
	RingBufferSize uint64
}

// Aggregator manages ring buffers and aggregation logic
type Aggregator struct {
	pb.UnimplementedHotTierServer // Embed for forward compatibility
	
	config Config
	
	// Ring buffers for different resolutions
	buffers1s  *ringbuffer.RingBuffer // 1-second resolution
	buffers10s *ringbuffer.RingBuffer // 10-second resolution
	buffers1m  *ringbuffer.RingBuffer // 1-minute resolution
	
	// Deduplication index
	dedupIndex sync.Map // map[string]time.Time
	
	// Stats
	eventsProcessed atomic.Uint64
	eventsDeduped   atomic.Uint64
	bytesProcessed  atomic.Uint64
	
	// NUMA node assignment (for future optimization)
	numaNode int
}

// NewAggregator creates a new aggregator instance
func NewAggregator(config Config) *Aggregator {
	// Calculate buffer sizes based on time windows
	// 24 hours = 86,400 seconds
	size1s := uint64(86400)  // 1 slot per second
	size10s := uint64(8640)  // 1 slot per 10 seconds
	size1m := uint64(1440)   // 1 slot per minute
	
	agg := &Aggregator{
		config:     config,
		buffers1s:  ringbuffer.NewRingBuffer(size1s, 1),
		buffers10s: ringbuffer.NewRingBuffer(size10s, 10),
		buffers1m:  ringbuffer.NewRingBuffer(size1m, 60),
		numaNode:   detectNUMANode(),
	}
	
	// Pin to NUMA node if available
	if agg.numaNode >= 0 {
		pinToNUMANode(agg.numaNode)
	}
	
	// Start background cleanup
	go agg.cleanupLoop()
	
	log.Infof("Aggregator initialized for shard %d with NUMA node %d",
		config.ShardID, agg.numaNode)
	
	return agg
}

// ApplyBatch processes a batch of events
func (agg *Aggregator) ApplyBatch(ctx context.Context, req *pb.ApplyBatchRequest) (*pb.ApplyBatchResponse, error) {
	startTime := time.Now()
	appliedCount := 0
	dedupedCount := 0
	
	// Process each record in the batch
	for _, record := range req.Records {
		// Check deduplication
		dedupKey := fmt.Sprintf("%s:%d", record.EventId, record.Revision)
		if _, exists := agg.dedupIndex.LoadOrStore(dedupKey, time.Now()); exists {
			dedupedCount++
			agg.eventsDeduped.Add(1)
			continue
		}
		
		// Convert to time series point
		point := ringbuffer.TimeSeriesPoint{
			Timestamp: record.EventTimeUs,
			GroupKey:  record.GroupKey,
			Value:     record.Metrics[0], // Simplified - take first metric
			UniqueID:  record.EventId,
		}
		
		// Write to appropriate buffers based on timestamp
		agg.buffers1s.Write(point.Timestamp, point.GroupKey, point.Value)
		
		// Aggregate to 10s buffer (10 million microseconds)
		bucket10s := (point.Timestamp / 10000000) * 10000000
		agg.buffers10s.Write(bucket10s, point.GroupKey, point.Value)
		
		// Aggregate to 1m buffer (60 million microseconds)
		bucket1m := (point.Timestamp / 60000000) * 60000000
		agg.buffers1m.Write(bucket1m, point.GroupKey, point.Value)
		
		appliedCount++
		agg.eventsProcessed.Add(1)
	}
	
	// Update stats
	agg.bytesProcessed.Add(uint64(len(req.Records) * 100)) // Approximate
	
	processingTime := time.Since(startTime)
	
	return &pb.ApplyBatchResponse{
		AppliedCount: int32(appliedCount),
		DedupedCount: int32(dedupedCount),
		RejectedCount: 0,
		CurrentEpoch: req.Epoch,
		WatermarkUs:  time.Now().UnixMicro(),
		ProcessingTimeUs: int32(processingTime.Microseconds()),
	}, nil
}

// Query executes a query against the aggregated data
func (agg *Aggregator) Query(ctx context.Context, req *pb.QueryRequest) (*pb.QueryResponse, error) {
	log.Debugf("Query received: StartUs=%d, EndUs=%d, GroupBy=%s", req.StartUs, req.EndUs, req.GroupBy)
	// Select appropriate buffer based on time range
	var buffer *ringbuffer.RingBuffer
	timeRange := req.EndUs - req.StartUs
	
	if timeRange <= 3600000000 { // <= 1 hour (3.6 billion microseconds), use 1s buffer
		buffer = agg.buffers1s
	} else if timeRange <= 21600000000 { // <= 6 hours (21.6 billion microseconds), use 10s buffer
		buffer = agg.buffers10s
	} else { // > 6 hours, use 1m buffer
		buffer = agg.buffers1m
	}
	
	// For simplicity, read the last N slots regardless of timestamp
	// In production, would maintain a timestamp index
	head := buffer.GetHead()
	tail := buffer.GetTail()
	
	// Read up to 1000 recent slots
	startPos := tail
	endPos := head
	if endPos-startPos > 1000 {
		startPos = endPos - 1000
	}
	
	// Read range from buffer
	slots := buffer.ReadRange(startPos, endPos)
	log.Debugf("Reading slots from %d to %d, got %d slots", startPos, endPos, len(slots))
	
	// Aggregate results
	results := make([]*pb.AggregateResult, 0, len(slots))
	for _, slot := range slots {
		// Filter by timestamp range
		if slot != nil {
			log.Debugf("Slot timestamp: %d, groups: %d", slot.Timestamp, len(slot.Groups))
		}
		if slot == nil || slot.Timestamp < req.StartUs || slot.Timestamp > req.EndUs {
			continue
		}
		
		for groupKey, group := range slot.Groups {
			log.Debugf("Checking group: key=%s, req.GroupBy=%s", groupKey, req.GroupBy)
			// Filter by query conditions
			if req.GroupBy != "" && req.GroupBy != "default" && groupKey != req.GroupBy {
				continue
			}
			
			result := &pb.AggregateResult{
				TimestampUs: slot.Timestamp,
				GroupKey:    groupKey,
				Sum:         uint64ToFloat64(group.Sum.Load()),
				Count:       int64(group.Count.Load()),
				Min:         uint64ToFloat64(group.Min.Load()),
				Max:         uint64ToFloat64(group.Max.Load()),
			}
			
			// Add sketch results if requested
			if req.IncludeUniques && group.HLL != nil {
				result.UniqueCount = int64(group.HLL.Estimate())
			}
			
			if req.IncludePercentiles && group.TDigest != nil {
				result.P50 = group.TDigest.Percentile(50)
				result.P95 = group.TDigest.Percentile(95)
				result.P99 = group.TDigest.Percentile(99)
			}
			
			results = append(results, result)
			log.Debugf("Added result: timestamp=%d, group=%s, count=%d", result.TimestampUs, result.GroupKey, result.Count)
		}
	}
	
	log.Debugf("Query returning %d results", len(results))
	
	return &pb.QueryResponse{
		Results:     results,
		WatermarkUs: time.Now().UnixMicro(),
	}, nil
}

// RegisterGRPC registers the aggregator with a gRPC server
func (agg *Aggregator) RegisterGRPC(server *grpc.Server) {
	pb.RegisterHotTierServer(server, agg)
}

// GetStats implements the gRPC GetStats method
func (agg *Aggregator) GetStats(ctx context.Context, req *pb.GetStatsRequest) (*pb.GetStatsResponse, error) {
	response := &pb.GetStatsResponse{
		EventsProcessed: int64(agg.eventsProcessed.Load()),
		EventsDeduped:   int64(agg.eventsDeduped.Load()),
		BytesProcessed:  int64(agg.bytesProcessed.Load()),
		ShardId:         int32(agg.config.ShardID),
		NumaNode:        int32(agg.numaNode),
	}
	
	if req.IncludeBufferStats {
		// Add buffer stats
		stats1s := agg.buffers1s.GetStats()
		response.Buffer_1S = &pb.BufferStats{
			SlotsUsed:   int64(stats1s["head"] - stats1s["tail"]),
			TotalSlots:  int64(stats1s["capacity"]),
			WritesTotal: int64(stats1s["writes"]),
			ReadsTotal:  int64(stats1s["reads"]),
		}
		
		stats10s := agg.buffers10s.GetStats()
		response.Buffer_10S = &pb.BufferStats{
			SlotsUsed:   int64(stats10s["head"] - stats10s["tail"]),
			TotalSlots:  int64(stats10s["capacity"]),
			WritesTotal: int64(stats10s["writes"]),
			ReadsTotal:  int64(stats10s["reads"]),
		}
		
		stats1m := agg.buffers1m.GetStats()
		response.Buffer_1M = &pb.BufferStats{
			SlotsUsed:   int64(stats1m["head"] - stats1m["tail"]),
			TotalSlots:  int64(stats1m["capacity"]),
			WritesTotal: int64(stats1m["writes"]),
			ReadsTotal:  int64(stats1m["reads"]),
		}
	}
	
	if req.IncludeMemoryStats {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		response.Memory = &pb.MemoryStats{
			AllocBytes:  int64(m.Alloc),
			SysBytes:    int64(m.Sys),
			GcRuns:      int32(m.NumGC),
			HeapObjects: int64(m.HeapObjects),
		}
	}
	
	return response, nil
}

// GetStatsMap returns aggregator statistics as a map (for internal use)
func (agg *Aggregator) GetStatsMap() map[string]interface{} {
	stats := map[string]interface{}{
		"events_processed": agg.eventsProcessed.Load(),
		"events_deduped":   agg.eventsDeduped.Load(),
		"bytes_processed":  agg.bytesProcessed.Load(),
		"shard_id":         agg.config.ShardID,
		"numa_node":        agg.numaNode,
	}
	
	// Add buffer stats
	stats["buffer_1s"] = agg.buffers1s.GetStats()
	stats["buffer_10s"] = agg.buffers10s.GetStats()
	stats["buffer_1m"] = agg.buffers1m.GetStats()
	
	// Memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stats["memory_alloc"] = m.Alloc
	stats["memory_sys"] = m.Sys
	stats["gc_runs"] = m.NumGC
	
	return stats
}

// cleanupLoop periodically cleans up old dedup entries
func (agg *Aggregator) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		cutoff := time.Now().Add(-24 * time.Hour)
		removed := 0
		
		agg.dedupIndex.Range(func(key, value interface{}) bool {
			if timestamp, ok := value.(time.Time); ok {
				if timestamp.Before(cutoff) {
					agg.dedupIndex.Delete(key)
					removed++
				}
			}
			return true
		})
		
		if removed > 0 {
			log.Debugf("Cleaned up %d old dedup entries", removed)
		}
	}
}

// detectNUMANode detects the NUMA node for CPU affinity
func detectNUMANode() int {
	// In production, this would read from /sys/devices/system/node/
	// For now, return -1 (no NUMA)
	return -1
}

// pinToNUMANode pins the process to a specific NUMA node
func pinToNUMANode(node int) {
	// In production, this would use syscalls to set CPU affinity
	// For now, this is a no-op
	log.Debugf("Would pin to NUMA node %d", node)
}

// Helper function for float64 conversion
func uint64ToFloat64(u uint64) float64 {
	// Check if this is an uninitialized value (all zeros)
	if u == 0 {
		return 0.0
	}
	
	// Convert using unsafe pointer
	f := *(*float64)(unsafe.Pointer(&u))
	
	// Check for NaN or Inf
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0.0
	}
	
	return f
}

// GetSnapshot returns a snapshot of the current state for persistence
func (agg *Aggregator) GetSnapshot() (interface{}, error) {
	// Create snapshot structure
	snapshot := map[string]interface{}{
		"shard_id":         agg.config.ShardID,
		"events_processed": agg.eventsProcessed.Load(),
		"events_deduped":   agg.eventsDeduped.Load(),
		"bytes_processed":  agg.bytesProcessed.Load(),
		"timestamp":        time.Now().Unix(),
	}
	
	// Note: In production, we would serialize the ring buffer data
	// For now, just return basic stats
	return snapshot, nil
}