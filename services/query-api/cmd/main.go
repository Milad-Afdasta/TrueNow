package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/query-api/internal/cache"
	"github.com/Milad-Afdasta/TrueNow/services/query-api/internal/planner"
	"github.com/Milad-Afdasta/TrueNow/services/query-api/internal/router"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	// Use all CPU cores
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Configuration
	config := &Config{
		HTTPPort:         getEnvOrDefault("HTTP_PORT", "8081"),
		HotTierEndpoints: []string{"localhost:9090"}, // In production, discover from control plane
		CacheEnabled:     true,
		CacheRedisAddr:   getEnvOrDefault("REDIS_ADDR", "localhost:6379"),
	}

	// Create components
	queryCache := cache.NewQueryCache(config.CacheRedisAddr, config.CacheEnabled)
	queryPlanner := planner.NewQueryPlanner()
	queryRouter := router.NewQueryRouter(config.HotTierEndpoints)

	// Create HTTP server
	r := mux.NewRouter()
	
	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	}).Methods("GET")

	// Query endpoint
	r.HandleFunc("/v1/query", handleQuery(queryCache, queryPlanner, queryRouter)).Methods("POST")
	
	// Stats endpoint
	r.HandleFunc("/v1/stats", handleStats(queryRouter)).Methods("GET")

	// Metrics endpoint (Prometheus format)
	r.HandleFunc("/metrics", handleMetrics()).Methods("GET")

	server := &http.Server{
		Addr:         ":" + config.HTTPPort,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server
	go func() {
		log.Infof("Query API starting on port %s", config.HTTPPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Wait for interrupt
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down Query API...")
	
	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	if err := server.Shutdown(ctx); err != nil {
		log.Errorf("Server forced to shutdown: %v", err)
	}

	// Close connections
	queryCache.Close()
	queryRouter.Close()

	log.Info("Query API exited")
}

// handleQuery processes query requests
func handleQuery(cache *cache.QueryCache, planner *planner.QueryPlanner, router *router.QueryRouter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Parse query request
		var req QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Validate query
		if err := validateQuery(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Check cache
		cacheKey := cache.GenerateKey(&req)
		if cached, found := cache.Get(cacheKey); found {
			w.Header().Set("X-Cache", "HIT")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cached)
			return
		}

		// Plan query - pass as interface{} since planner accepts that
		plan := planner.Plan(&req)
		
		// Execute query
		results, err := router.Execute(plan)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Cache results
		cache.Set(cacheKey, results, 60*time.Second)

		// Return results
		w.Header().Set("X-Cache", "MISS")
		w.Header().Set("X-Query-Time", time.Since(start).String())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

// handleStats returns query statistics
func handleStats(router *router.QueryRouter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := router.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

// handleMetrics returns Prometheus metrics
func handleMetrics() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// In production, use prometheus client library
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`# HELP query_requests_total Total number of query requests
# TYPE query_requests_total counter
query_requests_total 0

# HELP query_latency_seconds Query latency in seconds
# TYPE query_latency_seconds histogram
query_latency_seconds_bucket{le="0.1"} 0
query_latency_seconds_bucket{le="0.5"} 0
query_latency_seconds_bucket{le="1.0"} 0
query_latency_seconds_bucket{le="+Inf"} 0
`))
	}
}

// validateQuery validates query parameters
func validateQuery(req *QueryRequest) error {
	if req.StartTime >= req.EndTime {
		return fmt.Errorf("start_time must be before end_time")
	}
	
	// Max time range: 24 hours (in microseconds)
	if req.EndTime-req.StartTime > 86400000000 {
		return fmt.Errorf("time range exceeds 24 hours")
	}
	
	// Max groups: 10000
	if len(req.GroupBy) > 10000 {
		return fmt.Errorf("too many groups (max 10000)")
	}
	
	return nil
}

type Config struct {
	HTTPPort         string
	HotTierEndpoints []string
	CacheEnabled     bool
	CacheRedisAddr   string
}

type QueryRequest struct {
	Namespace string   `json:"namespace"`
	Table     string   `json:"table"`
	StartTime int64    `json:"start_time"`
	EndTime   int64    `json:"end_time"`
	GroupBy   []string `json:"group_by"`
	Metrics   []string `json:"metrics"`
	Filters   map[string]interface{} `json:"filters"`
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}