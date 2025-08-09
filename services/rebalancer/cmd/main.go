package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/api"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/coordinator"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/detector"
	grpcserver "github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/grpc"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/pkg/types"
)

func main() {
	// Parse command line flags
	var (
		port       = flag.Int("port", 8086, "HTTP server port")
		grpcPort   = flag.Int("grpc-port", 9086, "gRPC server port")
		enableGRPC = flag.Bool("enable-grpc", true, "Enable gRPC server")
		simulate   = flag.Bool("simulate", false, "Run in simulation mode")
		verbose    = flag.Bool("verbose", false, "Enable verbose logging")
	)
	flag.Parse()

	if *verbose {
		log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	}

	// Load configuration
	config := loadConfig()

	// Initialize components
	detector := detector.NewHotspotDetector(config)
	coordinator := coordinator.NewShardCoordinator(config)

	// Initialize shards if in simulation mode
	if *simulate {
		initializeSimulation(detector, coordinator, config)
	}

	// Create API handler
	handler := api.NewHandler(detector, coordinator, config)

	// Setup HTTP server
	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	// Add CORS middleware
	router.Use(corsMiddleware)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start background workers
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}

	// Start metrics collector
	if *simulate {
		wg.Add(1)
		go metricsCollector(ctx, wg, detector, config)
	}

	// Start rebalance worker
	wg.Add(1)
	go rebalanceWorker(ctx, wg, detector, coordinator, config)

	// Start salt cleanup worker
	wg.Add(1)
	go saltCleanupWorker(ctx, wg, coordinator, config)

	// Start servers
	go func() {
		log.Printf("🔄 Rebalancer service starting on port %d (simulate=%v)", *port, *simulate)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()
	
	// Start gRPC server if enabled
	if *enableGRPC {
		grpcSrv := grpcserver.NewServer(detector, coordinator, config, *grpcPort)
		go func() {
			log.Printf("🔄 Starting gRPC server on port %d", *grpcPort)
			if err := grpcSrv.Start(); err != nil {
				log.Fatalf("Failed to start gRPC server: %v", err)
			}
		}()
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down rebalancer service...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	// Cancel background workers
	cancel()
	wg.Wait()

	log.Println("Rebalancer service stopped")
}

func loadConfig() *types.RebalancerConfig {
	// In production, load from file or environment
	return &types.RebalancerConfig{
		// Thresholds
		HotShardThreshold:     1.5,  // 50% above average
		ColdShardThreshold:    0.5,  // 50% below average
		HotKeyThreshold:       0.1,  // 10% of shard traffic
		ImbalanceThreshold:    0.3,  // Gini coefficient threshold

		// Safety limits
		MaxLoadShift:          0.2, // Move max 20% of load at once
		CooldownPeriod:        5 * time.Minute,
		MaxConcurrentActions:  3,

		// Shard limits
		MinShardSize:          1000,
		MaxShardSize:          1000000,

		// Salting
		AutoSaltHotKeys:       true,
		SaltTTL:               1 * time.Hour,
		MaxSaltedKeysPerShard: 100,
	}
}

func initializeSimulation(detector *detector.HotspotDetector, coordinator *coordinator.ShardCoordinator, config *types.RebalancerConfig) {
	log.Println("Initializing simulation with 100 shards...")

	// Create initial shards
	epoch := coordinator.GetCurrentEpoch()
	
	// Initialize 100 physical shards with 10 virtual shards each
	for i := int32(0); i < 100; i++ {
		virtualShards := make([]int32, 10)
		for j := int32(0); j < 10; j++ {
			virtualShards[j] = i*10 + j
		}

		assignment := &types.ShardAssignment{
			ShardID:       i,
			VirtualShards: virtualShards,
			State:         types.ShardStateActive,
			Epoch:         epoch.Epoch,
		}

		epoch.Assignments[i] = assignment
		
		// Update virtual to physical mapping
		for _, vs := range virtualShards {
			epoch.VirtualToPhysical[vs] = i
		}
	}

	log.Printf("Created %d shards with %d virtual shards", len(epoch.Assignments), len(epoch.VirtualToPhysical))
}

func metricsCollector(ctx context.Context, wg *sync.WaitGroup, detector *detector.HotspotDetector, config *types.RebalancerConfig) {
	defer wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	shardCount := 100
	iteration := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			iteration++
			
			// Simulate metrics for each shard
			for i := 0; i < shardCount; i++ {
				shardID := int32(i)
				
				// Create realistic load distribution (power law)
				baseRPS := 1000.0
				
				// Make some shards hot (power law distribution)
				if i < 5 {
					baseRPS *= float64(20 - i*3) // Top shards get 20x, 17x, 14x, 11x, 8x load
				} else if i < 20 {
					baseRPS *= 2.0 // Next 15 shards get 2x load
				}
				
				// Add some randomness
				variance := (rand.Float64() - 0.5) * 0.2 * baseRPS
				rps := baseRPS + variance
				
				// Simulate periodic spikes
				if iteration%12 == 0 && i == 0 {
					rps *= 3.0 // Spike on shard 0 every minute
				}
				
				metrics := &types.ShardMetrics{
					ShardID:           shardID,
					RequestsPerSecond: rps,
					BytesPerSecond:    rps * 1024, // Assume 1KB per request
					KeyCount:          int64(10000 + rand.Intn(90000)),
					LastUpdate:        time.Now(),
				}
				
				// Simulate hot keys for hot shards
				if i < 3 {
					metrics.HotKeys = []types.HotKey{
						{
							Key:               fmt.Sprintf("user:%d", rand.Intn(100)),
							RequestsPerSecond: rps * 0.3,
							PercentOfShard:    0.3,
						},
						{
							Key:               fmt.Sprintf("product:%d", rand.Intn(100)),
							RequestsPerSecond: rps * 0.2,
							PercentOfShard:    0.2,
						},
					}
				}
				
				detector.UpdateMetrics(shardID, metrics)
			}
		}
	}
}

func rebalanceWorker(ctx context.Context, wg *sync.WaitGroup, detector *detector.HotspotDetector, coordinator *coordinator.ShardCoordinator, config *types.RebalancerConfig) {
	defer wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	lastAction := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if cooldown period has passed
			if time.Since(lastAction) < config.CooldownPeriod {
				continue
			}

			// Analyze current distribution
			dist := detector.AnalyzeDistribution()

			// Check if rebalancing is needed
			if dist.GiniCoefficient > config.ImbalanceThreshold {
				log.Printf("⚠️  High imbalance detected: Gini=%.3f (threshold=%.3f)", 
					dist.GiniCoefficient, config.ImbalanceThreshold)

				// Process recommendations
				for _, rec := range dist.Recommendations {
					if rec.Risk == types.RiskLevelHigh && rec.EstimatedImpact > 0.5 {
						log.Printf("📊 Recommendation: %s for shard %d (impact=%.2f)",
							rec.Action, rec.ShardID, rec.EstimatedImpact)
						
						// In production, would execute the recommendation
						// For now, just log it
						lastAction = time.Now()
						break // Only one action at a time
					}
				}
			}

			// Log current state
			if len(dist.HotShards) > 0 {
				log.Printf("🔥 Hot shards: %v (total: %d)", dist.HotShards[:min(5, len(dist.HotShards))], len(dist.HotShards))
			}
		}
	}
}

func saltCleanupWorker(ctx context.Context, wg *sync.WaitGroup, coordinator *coordinator.ShardCoordinator, config *types.RebalancerConfig) {
	defer wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed := coordinator.RemoveExpiredSalts()
			if removed > 0 {
				log.Printf("🧹 Removed %d expired salts", removed)
			}
		}
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}