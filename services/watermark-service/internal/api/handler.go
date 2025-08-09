package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/aggregator"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/store"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/pkg/types"
)

// Handler handles HTTP requests for watermark service
type Handler struct {
	store       *store.WatermarkStore
	aggregator  *aggregator.WatermarkAggregator
	logger      *logrus.Logger
	router      *mux.Router
	
	// Metrics
	requestDuration *prometheus.HistogramVec
	requestCount    *prometheus.CounterVec
}

// NewHandler creates a new API handler
func NewHandler(store *store.WatermarkStore, aggregator *aggregator.WatermarkAggregator, logger *logrus.Logger) *Handler {
	h := &Handler{
		store:      store,
		aggregator: aggregator,
		logger:     logger,
		router:     mux.NewRouter(),
	}
	
	// Initialize metrics
	h.requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "watermark_api_request_duration_seconds",
			Help:    "Duration of API requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint", "status"},
	)
	
	h.requestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watermark_api_request_total",
			Help: "Total number of API requests",
		},
		[]string{"method", "endpoint", "status"},
	)
	
	prometheus.MustRegister(h.requestDuration, h.requestCount)
	
	// Setup routes
	h.setupRoutes()
	
	return h
}

// setupRoutes configures all API routes
func (h *Handler) setupRoutes() {
	// Health endpoints
	h.router.HandleFunc("/health", h.handleHealth).Methods("GET")
	h.router.HandleFunc("/ready", h.handleReady).Methods("GET")
	
	// Watermark endpoints
	h.router.HandleFunc("/api/v1/watermark/system", h.handleGetSystemWatermark).Methods("GET")
	h.router.HandleFunc("/api/v1/watermark/table/{namespace}/{table}", h.handleGetTableWatermark).Methods("GET")
	h.router.HandleFunc("/api/v1/watermark/update", h.handleUpdateWatermark).Methods("POST")
	
	// Freshness endpoints
	h.router.HandleFunc("/api/v1/freshness", h.handleGetFreshness).Methods("GET")
	h.router.HandleFunc("/api/v1/freshness/config", h.handleSetFreshnessConfig).Methods("POST")
	h.router.HandleFunc("/api/v1/freshness/violations", h.handleGetViolations).Methods("GET")
	
	// Metrics endpoint
	h.router.Handle("/metrics", promhttp.Handler())
	
	// Wrap all routes with middleware
	h.router.Use(h.loggingMiddleware)
	h.router.Use(h.metricsMiddleware)
}

// ServeHTTP implements http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.router.ServeHTTP(w, r)
}

// loggingMiddleware logs all requests
func (h *Handler) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create response wrapper to capture status
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		next.ServeHTTP(wrapped, r)
		
		h.logger.WithFields(logrus.Fields{
			"method":   r.Method,
			"path":     r.URL.Path,
			"status":   wrapped.statusCode,
			"duration": time.Since(start).Milliseconds(),
			"remote":   r.RemoteAddr,
		}).Info("Request handled")
	})
}

// metricsMiddleware tracks metrics for all requests
func (h *Handler) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create response wrapper to capture status
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		next.ServeHTTP(wrapped, r)
		
		// Record metrics
		duration := time.Since(start).Seconds()
		status := statusClass(wrapped.statusCode)
		
		h.requestDuration.WithLabelValues(r.Method, r.URL.Path, status).Observe(duration)
		h.requestCount.WithLabelValues(r.Method, r.URL.Path, status).Inc()
	})
}

// handleHealth handles health check requests
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// handleReady handles readiness check requests
func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	status := h.store.GetHealthStatus()
	
	if !status.IsHealthy {
		h.writeJSON(w, http.StatusServiceUnavailable, status)
		return
	}
	
	h.writeJSON(w, http.StatusOK, status)
}

// handleGetSystemWatermark returns system-wide watermark state
func (h *Handler) handleGetSystemWatermark(w http.ResponseWriter, r *http.Request) {
	systemWatermark := h.store.GetSystemWatermark()
	
	// Optionally filter response based on query params
	includeDetails := r.URL.Query().Get("details") == "true"
	if !includeDetails {
		// Clear detailed table watermarks if not requested
		systemWatermark.TableWatermarks = nil
	}
	
	h.writeJSON(w, http.StatusOK, systemWatermark)
}

// handleGetTableWatermark returns watermark for a specific table
func (h *Handler) handleGetTableWatermark(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	table := vars["table"]
	
	if namespace == "" || table == "" {
		h.writeError(w, http.StatusBadRequest, "namespace and table are required")
		return
	}
	
	tableWatermark, exists := h.store.GetTableWatermark(namespace, table)
	if !exists {
		h.writeError(w, http.StatusNotFound, "table watermark not found")
		return
	}
	
	// Optionally include shard details
	includeShards := r.URL.Query().Get("shards") == "true"
	if !includeShards {
		tableWatermark.ShardWatermarks = nil
	}
	
	h.writeJSON(w, http.StatusOK, tableWatermark)
}

// handleUpdateWatermark handles watermark updates from services
func (h *Handler) handleUpdateWatermark(w http.ResponseWriter, r *http.Request) {
	var update types.WatermarkUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	
	// Validate update
	if update.ServiceID == "" || update.Namespace == "" || update.Table == "" {
		h.writeError(w, http.StatusBadRequest, "service_id, namespace, and table are required")
		return
	}
	
	// Set timestamp if not provided
	if update.Timestamp.IsZero() {
		update.Timestamp = time.Now()
	}
	
	// Update watermark
	h.store.UpdateServiceWatermark(&update)
	
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "accepted",
		"message": "watermark updated",
	})
}

// handleGetFreshness returns current freshness metrics
func (h *Handler) handleGetFreshness(w http.ResponseWriter, r *http.Request) {
	systemWatermark := h.store.GetSystemWatermark()
	
	response := map[string]interface{}{
		"global_freshness_p50_ms": systemWatermark.GlobalFreshnessP50,
		"global_freshness_p95_ms": systemWatermark.GlobalFreshnessP95,
		"global_freshness_p99_ms": systemWatermark.GlobalFreshnessP99,
		"total_events_per_sec":    systemWatermark.TotalEventsPerSec,
		"healthy_tables":          systemWatermark.HealthyTables,
		"total_tables":            systemWatermark.TotalTables,
		"timestamp":               time.Now(),
	}
	
	// Add per-table freshness if requested
	if r.URL.Query().Get("tables") == "true" {
		tables := make(map[string]map[string]int64)
		for key, tw := range systemWatermark.TableWatermarks {
			tables[key] = map[string]int64{
				"p50": tw.FreshnessP50,
				"p95": tw.FreshnessP95,
				"p99": tw.FreshnessP99,
			}
		}
		response["tables"] = tables
	}
	
	h.writeJSON(w, http.StatusOK, response)
}

// handleSetFreshnessConfig sets freshness SLO configuration
func (h *Handler) handleSetFreshnessConfig(w http.ResponseWriter, r *http.Request) {
	var config types.FreshnessConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	
	// Validate config
	if config.Namespace == "" || config.Table == "" {
		h.writeError(w, http.StatusBadRequest, "namespace and table are required")
		return
	}
	
	// Set default values if not provided
	if config.TargetFreshnessP99 == 0 {
		config.TargetFreshnessP99 = 30000 // 30 seconds default
	}
	if config.AlertThreshold == 0 {
		config.AlertThreshold = config.TargetFreshnessP99 * 2
	}
	if config.CriticalThreshold == 0 {
		config.CriticalThreshold = config.TargetFreshnessP99 * 4
	}
	
	h.store.SetFreshnessConfig(&config)
	
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": "freshness config updated",
		"config":  config,
	})
}

// handleGetViolations returns current SLO violations
func (h *Handler) handleGetViolations(w http.ResponseWriter, r *http.Request) {
	systemWatermark := h.store.GetSystemWatermark()
	
	response := map[string]interface{}{
		"violations": systemWatermark.ViolatedSLOs,
		"count":      len(systemWatermark.ViolatedSLOs),
		"timestamp":  time.Now(),
	}
	
	// Filter by severity if requested
	severity := r.URL.Query().Get("severity")
	if severity != "" {
		filtered := []types.SLOViolation{}
		for _, v := range systemWatermark.ViolatedSLOs {
			if v.Severity == severity {
				filtered = append(filtered, v)
			}
		}
		response["violations"] = filtered
		response["count"] = len(filtered)
	}
	
	h.writeJSON(w, http.StatusOK, response)
}

// Helper functions

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.WithError(err).Error("Failed to encode response")
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{
		"error":   message,
		"status":  http.StatusText(status),
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.ResponseWriter.WriteHeader(code)
		rw.written = true
	}
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(data)
}

func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "unknown"
	}
}