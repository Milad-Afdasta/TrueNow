package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/coordinator"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/detector"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/pkg/types"
)

// Handler handles HTTP API requests
type Handler struct {
	detector    *detector.HotspotDetector
	coordinator *coordinator.ShardCoordinator
	config      *types.RebalancerConfig
}

// NewHandler creates a new API handler
func NewHandler(detector *detector.HotspotDetector, coordinator *coordinator.ShardCoordinator, config *types.RebalancerConfig) *Handler {
	return &Handler{
		detector:    detector,
		coordinator: coordinator,
		config:      config,
	}
}

// RegisterRoutes registers all API routes
func (h *Handler) RegisterRoutes(router *mux.Router) {
	// Distribution endpoints
	router.HandleFunc("/api/v1/distribution", h.GetDistribution).Methods("GET")
	router.HandleFunc("/api/v1/distribution/gini", h.GetGiniCoefficient).Methods("GET")
	
	// Epoch endpoints
	router.HandleFunc("/api/v1/epoch", h.GetCurrentEpoch).Methods("GET")
	router.HandleFunc("/api/v1/epoch/history", h.GetEpochHistory).Methods("GET")
	
	// Shard endpoints
	router.HandleFunc("/api/v1/shards", h.GetShards).Methods("GET")
	router.HandleFunc("/api/v1/shards/{id}/metrics", h.GetShardMetrics).Methods("GET")
	router.HandleFunc("/api/v1/shards/{id}/hotkeys", h.GetHotKeys).Methods("GET")
	
	// Rebalance operations
	router.HandleFunc("/api/v1/rebalance", h.TriggerRebalance).Methods("POST")
	router.HandleFunc("/api/v1/rebalance/recommendations", h.GetRecommendations).Methods("GET")
	router.HandleFunc("/api/v1/rebalance/actions", h.GetActiveActions).Methods("GET")
	
	// Salt operations
	router.HandleFunc("/api/v1/salt", h.AddSalt).Methods("POST")
	router.HandleFunc("/api/v1/salt", h.RemoveSalt).Methods("DELETE")
	
	// Statistics
	router.HandleFunc("/api/v1/stats", h.GetStatistics).Methods("GET")
	
	// Health check
	router.HandleFunc("/health", h.HealthCheck).Methods("GET")
	
	// Metrics for Prometheus
	router.HandleFunc("/metrics", h.PrometheusMetrics).Methods("GET")
}

// GetDistribution returns current load distribution
func (h *Handler) GetDistribution(w http.ResponseWriter, r *http.Request) {
	dist := h.detector.AnalyzeDistribution()
	
	response := map[string]interface{}{
		"total_rps":         dist.TotalRPS,
		"total_bps":         dist.TotalBPS,
		"imbalance_ratio":   dist.ImbalanceRatio,
		"gini_coefficient":  dist.GiniCoefficient,
		"hot_shards":        dist.HotShards,
		"cold_shards":       dist.ColdShards,
		"shard_count":       len(dist.ShardMetrics),
		"recommendations":   dist.Recommendations,
		"last_analysis":     dist.LastAnalysis,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetGiniCoefficient returns just the Gini coefficient
func (h *Handler) GetGiniCoefficient(w http.ResponseWriter, r *http.Request) {
	dist := h.detector.AnalyzeDistribution()
	
	response := map[string]interface{}{
		"gini_coefficient": dist.GiniCoefficient,
		"interpretation":   interpretGini(dist.GiniCoefficient),
		"threshold":        h.config.ImbalanceThreshold,
		"exceeds_threshold": dist.GiniCoefficient > h.config.ImbalanceThreshold,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetCurrentEpoch returns current epoch configuration
func (h *Handler) GetCurrentEpoch(w http.ResponseWriter, r *http.Request) {
	epoch := h.coordinator.GetCurrentEpoch()
	
	response := map[string]interface{}{
		"epoch":                epoch.Epoch,
		"shard_count":          len(epoch.Assignments),
		"virtual_shard_count":  len(epoch.VirtualToPhysical),
		"created_at":           epoch.CreatedAt,
		"activated_at":         epoch.ActivatedAt,
		"reason":               epoch.Reason,
	}
	
	// Include shard mapping if requested
	if r.URL.Query().Get("include_mapping") == "true" {
		response["assignments"] = epoch.Assignments
		response["virtual_to_physical"] = epoch.VirtualToPhysical
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetEpochHistory returns historical epoch transitions
func (h *Handler) GetEpochHistory(w http.ResponseWriter, r *http.Request) {
	// For now, return current epoch only (in production, would query storage)
	epoch := h.coordinator.GetCurrentEpoch()
	
	history := []map[string]interface{}{
		{
			"epoch":        epoch.Epoch,
			"created_at":   epoch.CreatedAt,
			"activated_at": epoch.ActivatedAt,
			"reason":       epoch.Reason,
		},
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// GetShards returns all shard information
func (h *Handler) GetShards(w http.ResponseWriter, r *http.Request) {
	epoch := h.coordinator.GetCurrentEpoch()
	dist := h.detector.AnalyzeDistribution()
	
	shards := []map[string]interface{}{}
	for shardID, assignment := range epoch.Assignments {
		metrics := dist.ShardMetrics[shardID]
		
		shard := map[string]interface{}{
			"shard_id":           shardID,
			"state":              assignment.State,
			"virtual_shards":     len(assignment.VirtualShards),
			"key_range_start":    assignment.KeyRangeStart,
			"key_range_end":      assignment.KeyRangeEnd,
			"salted_keys_count":  len(assignment.SaltedKeys),
		}
		
		if metrics != nil {
			shard["requests_per_second"] = metrics.RequestsPerSecond
			shard["bytes_per_second"] = metrics.BytesPerSecond
			shard["key_count"] = metrics.KeyCount
			shard["hot_keys_count"] = len(metrics.HotKeys)
		}
		
		shards = append(shards, shard)
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(shards)
}

// GetShardMetrics returns metrics for a specific shard
func (h *Handler) GetShardMetrics(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shardID, err := strconv.ParseInt(vars["id"], 10, 32)
	if err != nil {
		http.Error(w, "Invalid shard ID", http.StatusBadRequest)
		return
	}
	
	dist := h.detector.AnalyzeDistribution()
	metrics, exists := dist.ShardMetrics[int32(shardID)]
	if !exists {
		http.Error(w, "Shard not found", http.StatusNotFound)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// GetHotKeys returns hot keys for a specific shard
func (h *Handler) GetHotKeys(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shardID, err := strconv.ParseInt(vars["id"], 10, 32)
	if err != nil {
		http.Error(w, "Invalid shard ID", http.StatusBadRequest)
		return
	}
	
	dist := h.detector.AnalyzeDistribution()
	metrics, exists := dist.ShardMetrics[int32(shardID)]
	if !exists {
		http.Error(w, "Shard not found", http.StatusNotFound)
		return
	}
	
	response := map[string]interface{}{
		"shard_id":  shardID,
		"hot_keys":  metrics.HotKeys,
		"threshold": h.config.HotKeyThreshold,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// TriggerRebalance manually triggers a rebalance operation
func (h *Handler) TriggerRebalance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string `json:"type"`
		ShardID int32  `json:"shard_id"`
		Reason  string `json:"reason"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	// For now, return a simulated response
	response := map[string]interface{}{
		"success": true,
		"message": "Rebalance operation initiated",
		"action": map[string]interface{}{
			"id":          "rebalance-" + strconv.FormatInt(time.Now().Unix(), 10),
			"type":        req.Type,
			"shard_id":    req.ShardID,
			"status":      "pending",
			"created_at":  time.Now(),
		},
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetRecommendations returns current rebalance recommendations
func (h *Handler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	dist := h.detector.AnalyzeDistribution()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dist.Recommendations)
}

// GetActiveActions returns currently active rebalance actions
func (h *Handler) GetActiveActions(w http.ResponseWriter, r *http.Request) {
	// For now, return empty list (in production, would query coordinator)
	actions := []interface{}{}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(actions)
}

// AddSalt adds salts to hot keys
func (h *Handler) AddSalt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShardID int32    `json:"shard_id"`
		Keys    []string `json:"keys"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	// Convert keys to HotKey type
	hotKeys := make([]types.HotKey, len(req.Keys))
	for i, key := range req.Keys {
		hotKeys[i] = types.HotKey{
			Key: key,
		}
	}
	
	if err := h.coordinator.AddSaltToHotKeys(req.ShardID, hotKeys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	response := map[string]interface{}{
		"success":     true,
		"shard_id":    req.ShardID,
		"salted_keys": len(req.Keys),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// RemoveSalt removes expired salts
func (h *Handler) RemoveSalt(w http.ResponseWriter, r *http.Request) {
	removed := h.coordinator.RemoveExpiredSalts()
	
	response := map[string]interface{}{
		"success":       true,
		"removed_count": removed,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetStatistics returns overall statistics
func (h *Handler) GetStatistics(w http.ResponseWriter, r *http.Request) {
	stats := h.coordinator.GetStatistics()
	dist := h.detector.AnalyzeDistribution()
	
	// Merge stats
	stats["total_rps"] = dist.TotalRPS
	stats["gini_coefficient"] = dist.GiniCoefficient
	stats["hot_shards_count"] = len(dist.HotShards)
	stats["cold_shards_count"] = len(dist.ColdShards)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HealthCheck returns service health
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":  "healthy",
		"service": "rebalancer",
		"uptime":  time.Since(startTime).Seconds(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// PrometheusMetrics returns metrics in Prometheus format
func (h *Handler) PrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	dist := h.detector.AnalyzeDistribution()
	stats := h.coordinator.GetStatistics()
	
	metrics := []string{
		"# HELP rebalancer_gini_coefficient Load distribution inequality (0=perfect, 1=worst)",
		"# TYPE rebalancer_gini_coefficient gauge",
		"rebalancer_gini_coefficient " + strconv.FormatFloat(dist.GiniCoefficient, 'f', 4, 64),
		"",
		"# HELP rebalancer_hot_shards Number of hot shards",
		"# TYPE rebalancer_hot_shards gauge",
		"rebalancer_hot_shards " + strconv.Itoa(len(dist.HotShards)),
		"",
		"# HELP rebalancer_cold_shards Number of cold shards",
		"# TYPE rebalancer_cold_shards gauge",
		"rebalancer_cold_shards " + strconv.Itoa(len(dist.ColdShards)),
		"",
		"# HELP rebalancer_total_rps Total requests per second",
		"# TYPE rebalancer_total_rps gauge",
		"rebalancer_total_rps " + strconv.FormatFloat(dist.TotalRPS, 'f', 2, 64),
		"",
		"# HELP rebalancer_current_epoch Current epoch number",
		"# TYPE rebalancer_current_epoch counter",
		"rebalancer_current_epoch " + strconv.FormatInt(stats["current_epoch"].(int64), 10),
		"",
		"# HELP rebalancer_split_count Total shard splits performed",
		"# TYPE rebalancer_split_count counter",
		"rebalancer_split_count " + strconv.FormatInt(stats["split_count"].(int64), 10),
		"",
		"# HELP rebalancer_merge_count Total shard merges performed",
		"# TYPE rebalancer_merge_count counter",
		"rebalancer_merge_count " + strconv.FormatInt(stats["merge_count"].(int64), 10),
		"",
	}
	
	w.Header().Set("Content-Type", "text/plain")
	for _, metric := range metrics {
		w.Write([]byte(metric + "\n"))
	}
}

// Helper functions

func interpretGini(gini float64) string {
	switch {
	case gini < 0.2:
		return "Excellent balance"
	case gini < 0.3:
		return "Good balance"
	case gini < 0.4:
		return "Moderate imbalance"
	case gini < 0.5:
		return "Significant imbalance"
	default:
		return "Severe imbalance"
	}
}

var startTime = time.Now()