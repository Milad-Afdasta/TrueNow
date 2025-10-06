package main

import (
	"context"
	"encoding/json"
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
	queryPlanner := planner.NewQueryPlanner(len(config.HotTierEndpoints))
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
	r.HandleFunc("/v1/stats", handleStats(queryRouter, queryCache)).Methods("GET")

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
func handleQuery(cache *cache.QueryCache, qp *planner.QueryPlanner, qr *router.QueryRouter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()

		var req planner.QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		plan, err := qp.Plan(&req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		cacheKey := plan.CacheKey
		if cache != nil {
			var cached router.QueryResult
			found, err := cache.Get(ctx, cacheKey, &cached)
			if err == nil && found {
				w.Header().Set("X-Cache", "HIT")
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(&cached)
				return
			}
		}

		results, err := qr.Execute(ctx, plan)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if cache != nil {
			_ = cache.Set(ctx, cacheKey, results, plan.Timeout)
		}

		w.Header().Set("X-Cache", "MISS")
		w.Header().Set("X-Query-Time", time.Since(start).String())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

// handleStats returns query statistics
func handleStats(qr *router.QueryRouter, cache *cache.QueryCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := map[string]interface{}{
			"router": qr.GetStats(),
		}
		if cache != nil {
			stats["cache"] = cache.GetStats()
		}
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

type Config struct {
	HTTPPort         string
	HotTierEndpoints []string
	CacheEnabled     bool
	CacheRedisAddr   string
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
