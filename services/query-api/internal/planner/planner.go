package planner

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	maxRangeMicroseconds int64 = int64(24 * time.Hour / time.Microsecond)
	defaultShardCount          = 1
)

// QueryRequest represents a query request
type QueryRequest struct {
	Namespace string                 `json:"namespace"`
	Table     string                 `json:"table"`
	StartTime int64                  `json:"start_time"`
	EndTime   int64                  `json:"end_time"`
	GroupBy   []string               `json:"group_by"`
	Metrics   []string               `json:"metrics"`
	Filters   map[string]interface{} `json:"filters"`
}

// Normalize applies defaults to the request.
func (qr *QueryRequest) Normalize() {
	if qr.Filters == nil {
		qr.Filters = map[string]interface{}{}
	}
}

// Validate validates the request.
func (qr *QueryRequest) Validate() error {
	if qr.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if qr.Table == "" {
		return fmt.Errorf("table is required")
	}
	if qr.StartTime <= 0 || qr.EndTime <= 0 {
		return fmt.Errorf("start_time and end_time must be > 0")
	}
	if qr.StartTime >= qr.EndTime {
		return fmt.Errorf("start_time must be before end_time")
	}
	if qr.EndTime-qr.StartTime > maxRangeMicroseconds {
		return fmt.Errorf("time range exceeds 24 hours")
	}
	if len(qr.GroupBy) > 10000 {
		return fmt.Errorf("too many group_by dimensions")
	}
	return nil
}

// QueryPlanner creates execution plans for queries
type QueryPlanner struct {
	shardCount int
}

// NewQueryPlanner creates a new query planner
func NewQueryPlanner(shardCount int) *QueryPlanner {
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	return &QueryPlanner{shardCount: shardCount}
}

// Plan creates an execution plan for a query
func (qp *QueryPlanner) Plan(req *QueryRequest) (*QueryPlan, error) {
	if req == nil {
		return nil, fmt.Errorf("nil query request")
	}

	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, err
	}

	timeRange := req.EndTime - req.StartTime
	resolution := Resolution1s
	if timeRange > int64(6*time.Hour/time.Microsecond) {
		resolution = Resolution1m
	} else if timeRange > int64(time.Hour/time.Microsecond) {
		resolution = Resolution10s
	}

	shards := qp.getShardsForQuery(req)

	plan := &QueryPlan{
		StartTime:  req.StartTime,
		EndTime:    req.EndTime,
		Namespace:  req.Namespace,
		Table:      req.Table,
		GroupBy:    append([]string(nil), req.GroupBy...),
		Metrics:    append([]string(nil), req.Metrics...),
		Filters:    req.Filters,
		Resolution: resolution,
		Shards:     shards,
		Parallel:   len(shards) > 1,
	}

	plan.CacheKey = qp.generateCacheKey(plan)
	qp.optimize(plan)

	log.Debugf("Query plan: resolution=%s, shards=%v, parallel=%v", plan.Resolution, plan.Shards, plan.Parallel)
	return plan, nil
}

func (qp *QueryPlanner) getShardsForQuery(req *QueryRequest) []int {
	shards := make([]int, qp.shardCount)
	for i := 0; i < qp.shardCount; i++ {
		shards[i] = i
	}
	return shards
}

// optimize optimizes the query plan
func (qp *QueryPlanner) optimize(plan *QueryPlan) {
	sort.Ints(plan.Shards)
	plan.PredicatePushdown = true
	plan.ProjectionPushdown = true
	plan.UseBloomFilter = plan.Resolution == Resolution1s

	if len(plan.Shards) > 10 {
		plan.Timeout = 30 * time.Second
	} else {
		plan.Timeout = 10 * time.Second
	}
}

// generateCacheKey generates a cache key for the query
func (qp *QueryPlanner) generateCacheKey(plan *QueryPlan) string {
	type cachePayload struct {
		Namespace  string     `json:"ns"`
		Table      string     `json:"tbl"`
		Start      int64      `json:"start"`
		End        int64      `json:"end"`
		GroupBy    []string   `json:"group"`
		Metrics    []string   `json:"metrics"`
		Filters    []string   `json:"filters"`
		Resolution Resolution `json:"resolution"`
	}

	payload := cachePayload{
		Namespace:  plan.Namespace,
		Table:      plan.Table,
		Start:      plan.StartTime,
		End:        plan.EndTime,
		GroupBy:    canonicalStrings(plan.GroupBy),
		Metrics:    canonicalStrings(plan.Metrics),
		Filters:    canonicalFilters(plan.Filters),
		Resolution: plan.Resolution,
	}

	data, _ := json.Marshal(payload)
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf("query:%x", h.Sum64())
}

func canonicalStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	clone := append([]string(nil), values...)
	sort.Strings(clone)
	return clone
}

func canonicalFilters(filters map[string]interface{}) []string {
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
		val, _ := json.Marshal(filters[k])
		result = append(result, fmt.Sprintf("%s=%s", k, strings.TrimSpace(string(val))))
	}
	return result
}

// QueryPlan represents an execution plan
type QueryPlan struct {
	StartTime          int64
	EndTime            int64
	Namespace          string
	Table              string
	GroupBy            []string
	Metrics            []string
	Filters            map[string]interface{}
	Resolution         Resolution
	Shards             []int
	Parallel           bool
	PredicatePushdown  bool
	ProjectionPushdown bool
	UseBloomFilter     bool
	CacheKey           string
	Timeout            time.Duration
}

// Resolution represents time resolution
type Resolution string

const (
	Resolution1s  Resolution = "1s"
	Resolution10s Resolution = "10s"
	Resolution1m  Resolution = "1m"
)

// Cost estimates query cost
func (qp *QueryPlanner) Cost(plan *QueryPlan) int64 {
	// Estimate based on:
	// - Time range
	// - Number of shards
	// - Resolution
	// - Number of groups

	timeSlots := (plan.EndTime - plan.StartTime) / 1000 // seconds
	switch plan.Resolution {
	case Resolution10s:
		timeSlots /= 10
	case Resolution1m:
		timeSlots /= 60
	}

	shardCost := int64(len(plan.Shards))

	return timeSlots * shardCost
}
