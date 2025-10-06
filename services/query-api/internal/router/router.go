package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/query-api/internal/planner"
	pb "github.com/Milad-Afdasta/TrueNow/shared/proto/pb/hottier"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

var errNoEndpoints = errors.New("query router: no hot-tier endpoints configured")

// QueryRouter routes queries to hot-tier shards
type QueryRouter struct {
	endpoints []string
	clients   []pb.HotTierClient
	conns     []*grpc.ClientConn

	queries atomic.Uint64
	errors  atomic.Uint64
	latency atomic.Uint64 // microseconds

	mu sync.RWMutex
}

// NewQueryRouter creates a new query router
func NewQueryRouter(endpoints []string) *QueryRouter {
	if len(endpoints) == 0 {
		endpoints = []string{"localhost:9090"}
	}
	copyEndpoints := append([]string(nil), endpoints...)

	qr := &QueryRouter{
		endpoints: copyEndpoints,
		clients:   make([]pb.HotTierClient, len(copyEndpoints)),
		conns:     make([]*grpc.ClientConn, len(copyEndpoints)),
	}

	for i := range copyEndpoints {
		if err := qr.ensureClient(i); err != nil {
			log.WithError(err).Warnf("query router: initial connect failed for %s", copyEndpoints[i])
		}
	}

	log.Infof("Query router configured with %d hot-tier endpoints", len(copyEndpoints))
	return qr
}

func (qr *QueryRouter) ensureClient(idx int) error {
	if idx < 0 || idx >= len(qr.endpoints) {
		return fmt.Errorf("query router: endpoint index out of range: %d", idx)
	}

	qr.mu.RLock()
	client := qr.clients[idx]
	qr.mu.RUnlock()
	if client != nil {
		return nil
	}

	endpoint := qr.endpoints[idx]
	conn, err := qr.connect(endpoint)
	if err != nil {
		return err
	}

	qr.mu.Lock()
	defer qr.mu.Unlock()

	if qr.conns[idx] != nil {
		_ = qr.conns[idx].Close()
	}
	qr.conns[idx] = conn
	qr.clients[idx] = pb.NewHotTierClient(conn)
	log.Infof("Query router connected to %s", endpoint)
	return nil
}

func (qr *QueryRouter) connect(endpoint string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(100 * 1024 * 1024),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	return grpc.DialContext(ctx, endpoint, opts...)
}

// Execute executes a query plan
func (qr *QueryRouter) Execute(ctx context.Context, plan *planner.QueryPlan) (*QueryResult, error) {
	start := time.Now()
	qr.queries.Add(1)

	if plan == nil {
		qr.errors.Add(1)
		return nil, fmt.Errorf("query router: nil plan")
	}
	if len(qr.endpoints) == 0 {
		qr.errors.Add(1)
		return nil, errNoEndpoints
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if plan.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, plan.Timeout)
		defer cancel()
	}

	responses := make([]*pb.QueryResponse, len(plan.Shards))
	errCh := make(chan error, len(plan.Shards))
	var wg sync.WaitGroup

	for i, shardID := range plan.Shards {
		wg.Add(1)
		go func(index int, shard int) {
			defer wg.Done()
			resp, err := qr.queryShard(ctx, plan, shard)
			if err != nil {
				errCh <- err
				return
			}
			responses[index] = resp
		}(i, shardID)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			qr.errors.Add(1)
			return nil, err
		}
	}

	result := mergeResponses(plan, responses)
	result.QueryTimeMs = time.Since(start).Milliseconds()
	qr.latency.Store(uint64(time.Since(start).Microseconds()))
	return result, nil
}

func (qr *QueryRouter) queryShard(ctx context.Context, plan *planner.QueryPlan, shard int) (*pb.QueryResponse, error) {
	client, err := qr.clientForShard(shard)
	if err != nil {
		return nil, err
	}

	req := &pb.QueryRequest{
		StartUs:            plan.StartTime,
		EndUs:              plan.EndTime,
		Namespace:          plan.Namespace,
		Table:              plan.Table,
		GroupBy:            firstGroup(plan.GroupBy),
		Metrics:            plannerCanonical(plan.Metrics),
		Filters:            filtersToStrings(plan.Filters),
		IncludeUniques:     true,
		IncludePercentiles: true,
	}

	resp, err := client.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("shard %d query failed: %w", shard, err)
	}
	return resp, nil
}

func (qr *QueryRouter) clientForShard(shard int) (pb.HotTierClient, error) {
	if len(qr.endpoints) == 0 {
		return nil, errNoEndpoints
	}
	idx := shard % len(qr.endpoints)
	if err := qr.ensureClient(idx); err != nil {
		return nil, err
	}
	qr.mu.RLock()
	client := qr.clients[idx]
	qr.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("query router: client unavailable for shard %d", shard)
	}
	return client, nil
}

func mergeResponses(plan *planner.QueryPlan, responses []*pb.QueryResponse) *QueryResult {
	if len(responses) == 0 {
		return &QueryResult{}
	}

	type aggKey struct {
		timestamp int64
		group     string
	}

	type aggVal struct {
		sum         float64
		count       int64
		min         float64
		max         float64
		unique      int64
		weightedP50 float64
		weightedP95 float64
		weightedP99 float64
		weight      float64
	}

	rows := make(map[aggKey]*aggVal)
	watermark := responses[0].GetWatermarkUs()

	for _, resp := range responses {
		if resp == nil {
			continue
		}
		if resp.WatermarkUs < watermark {
			watermark = resp.WatermarkUs
		}
		for _, r := range resp.Results {
			key := aggKey{timestamp: r.TimestampUs, group: r.GroupKey}
			entry, ok := rows[key]
			if !ok {
				entry = &aggVal{min: math.MaxFloat64, max: -math.MaxFloat64}
				rows[key] = entry
			}
			entry.sum += r.Sum
			entry.count += r.Count
			if r.Min < entry.min {
				entry.min = r.Min
			}
			if r.Max > entry.max {
				entry.max = r.Max
			}
			entry.unique += r.UniqueCount
			weight := float64(r.Count)
			if weight <= 0 {
				weight = 1
			}
			entry.weight += weight
			entry.weightedP50 += r.P50 * weight
			entry.weightedP95 += r.P95 * weight
			entry.weightedP99 += r.P99 * weight
		}
	}

	result := &QueryResult{
		Rows:        make([]ResultRow, 0, len(rows)),
		WatermarkUs: watermark,
	}

	for key, val := range rows {
		row := ResultRow{
			Timestamp:   key.timestamp,
			Group:       key.group,
			Sum:         cleanFloat(val.sum),
			Count:       val.count,
			Min:         cleanFloat(val.min),
			Max:         cleanFloat(val.max),
			UniqueCount: val.unique,
		}
		if val.count == 0 {
			row.Min = 0
			row.Max = 0
		}
		if val.weight > 0 {
			row.P50 = cleanFloat(val.weightedP50 / val.weight)
			row.P95 = cleanFloat(val.weightedP95 / val.weight)
			row.P99 = cleanFloat(val.weightedP99 / val.weight)
		}
		result.Rows = append(result.Rows, row)
	}

	sort.Slice(result.Rows, func(i, j int) bool {
		if result.Rows[i].Timestamp == result.Rows[j].Timestamp {
			return result.Rows[i].Group < result.Rows[j].Group
		}
		return result.Rows[i].Timestamp < result.Rows[j].Timestamp
	})

	return result
}

func filtersToStrings(filters map[string]interface{}) []string {
	if len(filters) == 0 {
		return nil
	}
	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, fmt.Sprintf("%s=%v", k, filters[k]))
	}
	return result
}

func firstGroup(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	return groups[0]
}

func plannerCanonical(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	clone := append([]string(nil), values...)
	sort.Strings(clone)
	return clone
}

// Close closes all connections
func (qr *QueryRouter) Close() {
	qr.mu.Lock()
	defer qr.mu.Unlock()

	for i, conn := range qr.conns {
		if conn != nil {
			_ = conn.Close()
		}
		qr.conns[i] = nil
		qr.clients[i] = nil
	}

	log.Info("Query router closed")
}

// GetStats returns router statistics
func (qr *QueryRouter) GetStats() map[string]uint64 {
	qr.mu.RLock()
	active := uint64(0)
	for _, client := range qr.clients {
		if client != nil {
			active++
		}
	}
	qr.mu.RUnlock()

	return map[string]uint64{
		"queries":      qr.queries.Load(),
		"errors":       qr.errors.Load(),
		"latency_us":   qr.latency.Load(),
		"active_nodes": active,
	}
}

// cleanFloat ensures float values are JSON-serializable
func cleanFloat(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0.0
	}
	return f
}

// ResultRow represents a single row in query results
type ResultRow struct {
	Timestamp   int64   `json:"timestamp"`
	Group       string  `json:"group"`
	Sum         float64 `json:"sum"`
	Count       int64   `json:"count"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
	UniqueCount int64   `json:"unique_count"`
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	P99         float64 `json:"p99"`
}

// QueryResult represents query results
type QueryResult struct {
	Rows        []ResultRow `json:"results"`
	WatermarkUs int64       `json:"watermark_us"`
	QueryTimeMs int64       `json:"query_time_ms"`
}
