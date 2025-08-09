package planner

import (
	"encoding/json"
	"hash/fnv"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"
)

// QueryPlanner creates execution plans for queries
type QueryPlanner struct {
	// Shard mapping cache
	shardMap map[string][]int
}

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

// NewQueryPlanner creates a new query planner
func NewQueryPlanner() *QueryPlanner {
	return &QueryPlanner{
		shardMap: make(map[string][]int),
	}
}

// Plan creates an execution plan for a query
func (qp *QueryPlanner) Plan(req interface{}) *QueryPlan {
	// Extract query parameters from the request
	var namespace, table string
	var startTime, endTime int64
	var groupBy, metrics []string
	var filters map[string]interface{}
	
	// Use reflection to get fields from any struct with these fields
	if reqMap, ok := req.(map[string]interface{}); ok {
		namespace, _ = reqMap["namespace"].(string)
		table, _ = reqMap["table"].(string)
		startTime, _ = reqMap["start_time"].(int64)
		endTime, _ = reqMap["end_time"].(int64)
		groupBy, _ = reqMap["group_by"].([]string)
		metrics, _ = reqMap["metrics"].([]string)
		filters, _ = reqMap["filters"].(map[string]interface{})
	} else {
		// Try to extract using JSON marshaling/unmarshaling
		data, _ := json.Marshal(req)
		var reqData map[string]interface{}
		json.Unmarshal(data, &reqData)
		
		namespace, _ = reqData["namespace"].(string)
		table, _ = reqData["table"].(string)
		startTime = int64(reqData["start_time"].(float64))
		endTime = int64(reqData["end_time"].(float64))
		
		if gb, ok := reqData["group_by"].([]interface{}); ok {
			for _, g := range gb {
				groupBy = append(groupBy, g.(string))
			}
		}
		if m, ok := reqData["metrics"].([]interface{}); ok {
			for _, metric := range m {
				metrics = append(metrics, metric.(string))
			}
		}
		filters, _ = reqData["filters"].(map[string]interface{})
	}
	
	// Determine resolution based on time range
	timeRange := endTime - startTime
	var resolution Resolution
	
	if timeRange <= 3600000000 { // <= 1 hour (3.6 billion microseconds)
		resolution = Resolution1s
	} else if timeRange <= 21600000000 { // <= 6 hours (21.6 billion microseconds)
		resolution = Resolution10s
	} else {
		resolution = Resolution1m
	}
	
	// Determine shards to query
	shards := qp.getShardsForQuery(req)
	
	// Create plan with query details
	plan := &QueryPlan{
		StartTime:  startTime,
		EndTime:    endTime,
		Namespace:  namespace,
		Table:      table,
		GroupBy:    groupBy,
		Metrics:    metrics,
		Filters:    filters,
		Resolution: resolution,
		Shards:     shards,
		Parallel:   len(shards) > 1,
		CacheKey:   qp.generateCacheKey(req),
	}
	
	// Optimize plan
	qp.optimize(plan)
	
	log.Debugf("Query plan: resolution=%s, shards=%v, parallel=%v",
		resolution, shards, plan.Parallel)
	
	return plan
}

// getShardsForQuery determines which shards to query
func (qp *QueryPlanner) getShardsForQuery(req interface{}) []int {
	// In production, this would:
	// 1. Look up namespace/table in control plane
	// 2. Get shard assignments from registry
	// 3. Filter based on query predicates
	
	// For now, return all shards
	return []int{0, 1}
}

// optimize optimizes the query plan
func (qp *QueryPlanner) optimize(plan *QueryPlan) {
	// Sort shards for consistent ordering
	sort.Ints(plan.Shards)
	
	// Enable predicate pushdown
	plan.PredicatePushdown = true
	
	// Enable projection pushdown (only fetch needed columns)
	plan.ProjectionPushdown = true
	
	// Determine if we can use bloom filters
	plan.UseBloomFilter = plan.Resolution == Resolution1s
	
	// Set timeout based on complexity
	if len(plan.Shards) > 10 {
		plan.Timeout = 30 * time.Second
	} else {
		plan.Timeout = 10 * time.Second
	}
}

// generateCacheKey generates a cache key for the query
func (qp *QueryPlanner) generateCacheKey(req interface{}) string {
	h := fnv.New64a()
	// Simplified - in production, serialize request properly
	h.Write([]byte("query"))
	return string(h.Sum64())
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