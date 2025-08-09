package router

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/query-api/internal/planner"
	pb "github.com/Milad-Afdasta/TrueNow/shared/proto/pb/hottier"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// QueryRouter routes queries to hot-tier shards
type QueryRouter struct {
	endpoints []string
	clients   []pb.HotTierClient
	conns     []*grpc.ClientConn
	
	// Stats
	queries   atomic.Uint64
	errors    atomic.Uint64
	cacheHits atomic.Uint64
	latency   atomic.Uint64 // microseconds
	
	mu sync.RWMutex
}

// NewQueryRouter creates a new query router
func NewQueryRouter(endpoints []string) *QueryRouter {
	qr := &QueryRouter{
		endpoints: endpoints,
		clients:   make([]pb.HotTierClient, len(endpoints)),
		conns:     make([]*grpc.ClientConn, len(endpoints)),
	}
	
	// Connect to all endpoints
	for i, endpoint := range endpoints {
		conn, err := qr.connect(endpoint)
		if err != nil {
			log.Errorf("Failed to connect to hot-tier %s: %v", endpoint, err)
			continue
		}
		
		qr.conns[i] = conn
		qr.clients[i] = pb.NewHotTierClient(conn)
	}
	
	log.Infof("Query router connected to %d hot-tier endpoints", len(endpoints))
	
	return qr
}

// connect establishes gRPC connection
func (qr *QueryRouter) connect(endpoint string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(100 * 1024 * 1024), // 100MB
		),
	}
	
	return grpc.DialContext(ctx, endpoint, opts...)
}

// Execute executes a query plan
func (qr *QueryRouter) Execute(plan interface{}) (*QueryResult, error) {
	start := time.Now()
	qr.queries.Add(1)
	
	// Import the planner package for QueryPlan type
	// Type assert to get the actual plan from planner
	queryPlan, ok := plan.(*planner.QueryPlan)
	if !ok {
		qr.errors.Add(1)
		return nil, fmt.Errorf("invalid plan type: %T", plan)
	}
	
	// For simplified implementation, query first shard only
	// In production, fan out to all shards and merge results
	
	if len(qr.clients) == 0 {
		qr.errors.Add(1)
		return nil, fmt.Errorf("no available hot-tier nodes")
	}
	
	// Create query request using actual plan
	groupBy := ""
	if len(queryPlan.GroupBy) > 0 {
		groupBy = queryPlan.GroupBy[0]
	}
	
	req := &pb.QueryRequest{
		StartUs:            queryPlan.StartTime, // In microseconds
		EndUs:              queryPlan.EndTime,   // In microseconds
		GroupBy:            groupBy,
		Metrics:            []string{"count", "sum"},
		IncludeUniques:     true,
		IncludePercentiles: true,
	}
	
	// Query first client (in production, query all shards)
	client := qr.clients[0]
	if client == nil {
		qr.errors.Add(1)
		return nil, fmt.Errorf("client not available")
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	resp, err := client.Query(ctx, req)
	if err != nil {
		qr.errors.Add(1)
		return nil, fmt.Errorf("query failed: %w", err)
	}
	
	// Convert response
	result := &QueryResult{
		Results:     make([]map[string]interface{}, 0, len(resp.Results)),
		WatermarkUs: resp.WatermarkUs,
		QueryTimeMs: time.Since(start).Milliseconds(),
	}
	
	for _, r := range resp.Results {
		result.Results = append(result.Results, map[string]interface{}{
			"timestamp":    r.TimestampUs,
			"group":        r.GroupKey,
			"count":        r.Count,
			"sum":          cleanFloat(r.Sum),
			"min":          cleanFloat(r.Min),
			"max":          cleanFloat(r.Max),
			"unique_count": r.UniqueCount,
			"p50":          cleanFloat(r.P50),
			"p95":          cleanFloat(r.P95),
			"p99":          cleanFloat(r.P99),
		})
	}
	
	// Update stats
	qr.latency.Store(uint64(time.Since(start).Microseconds()))
	
	log.Debugf("Query executed in %v, returned %d results",
		time.Since(start), len(result.Results))
	
	return result, nil
}

// MergeResults merges results from multiple shards
func (qr *QueryRouter) MergeResults(results []*QueryResult) *QueryResult {
	if len(results) == 0 {
		return &QueryResult{}
	}
	
	if len(results) == 1 {
		return results[0]
	}
	
	// Merge logic:
	// 1. Combine results by timestamp and group
	// 2. Sum counts and sums
	// 3. Recalculate min/max
	// 4. Merge HLL sketches for uniques
	// 5. Merge T-Digest for percentiles
	
	merged := &QueryResult{
		Results:     make([]map[string]interface{}, 0),
		WatermarkUs: results[0].WatermarkUs,
	}
	
	// Simplified merge - in production, implement proper aggregation
	for _, result := range results {
		merged.Results = append(merged.Results, result.Results...)
		merged.QueryTimeMs += result.QueryTimeMs
		
		// Update watermark to minimum
		if result.WatermarkUs < merged.WatermarkUs {
			merged.WatermarkUs = result.WatermarkUs
		}
	}
	
	return merged
}

// Close closes all connections
func (qr *QueryRouter) Close() {
	qr.mu.Lock()
	defer qr.mu.Unlock()
	
	for _, conn := range qr.conns {
		if conn != nil {
			conn.Close()
		}
	}
	
	log.Info("Query router closed")
}

// GetStats returns router statistics
func (qr *QueryRouter) GetStats() map[string]uint64 {
	return map[string]uint64{
		"queries":      qr.queries.Load(),
		"errors":       qr.errors.Load(),
		"cache_hits":   qr.cacheHits.Load(),
		"latency_us":   qr.latency.Load(),
		"active_nodes": uint64(len(qr.clients)),
	}
}

// cleanFloat ensures float values are JSON-serializable
func cleanFloat(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0.0
	}
	return f
}

// QueryResult represents query results
type QueryResult struct {
	Results     []map[string]interface{} `json:"results"`
	WatermarkUs int64                    `json:"watermark_us"`
	QueryTimeMs int64                    `json:"query_time_ms"`
}