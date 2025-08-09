package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/hot-tier/internal/aggregator"
	"github.com/Milad-Afdasta/TrueNow/services/hot-tier/internal/snapshot"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.DebugLevel)

	// Configure runtime for maximum performance
	configureRuntime()

	// Create hot-tier components
	aggregator := aggregator.NewAggregator(aggregator.Config{
		ShardID:        getShardID(),
		NumCPU:         runtime.NumCPU(),
		MemoryGB:       getMemoryLimit(),
		RingBufferSize: 86400, // 24 hours at 1-second resolution
	})

	// Create snapshot manager
	snapshotter := snapshot.NewManager(snapshot.Config{
		Interval:    5 * time.Minute,
		StoragePath: "/tmp/hot-tier/snapshots",
		Compression: true,
	})

	// Start gRPC server
	listener, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Configure gRPC server for high performance
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(100 * 1024 * 1024),        // 100MB max message
		grpc.MaxSendMsgSize(100 * 1024 * 1024),        // 100MB max message
		grpc.MaxConcurrentStreams(10000),              // High concurrency
		grpc.ConnectionTimeout(30 * time.Second),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 1 * time.Minute,
			Time:                  1 * time.Minute,
			Timeout:               20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	grpcServer := grpc.NewServer(opts...)
	
	// Register services
	aggregator.RegisterGRPC(grpcServer)

	// Start server in goroutine
	go func() {
		log.Infof("Hot-tier starting on :9090 with %d CPUs, %.2f GB memory",
			runtime.NumCPU(), float64(getMemoryLimit())/1024/1024/1024)
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Start snapshot routine
	ctx, cancel := context.WithCancel(context.Background())
	go snapshotter.Start(ctx, aggregator)

	// Start metrics server
	go startMetricsServer()

	// Wait for interrupt
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down hot-tier...")
	cancel()
	
	// Graceful shutdown
	grpcServer.GracefulStop()
	
	// Final snapshot
	if err := snapshotter.TakeSnapshot(aggregator); err != nil {
		log.Errorf("Failed to take final snapshot: %v", err)
	}

	log.Info("Hot-tier exited")
}

// configureRuntime optimizes Go runtime for high performance
func configureRuntime() {
	// Use all available CPUs
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Set memory limit to prevent OOM
	memLimit := getMemoryLimit()
	debug.SetMemoryLimit(int64(memLimit))

	// Optimize GC for low latency
	debug.SetGCPercent(100) // Default is 100, lower = more frequent GC

	// Set max threads
	debug.SetMaxThreads(10000)

	// Increase file descriptor limit
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
		rLimit.Cur = rLimit.Max
		syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	}

	log.Infof("Runtime configured: CPUs=%d, MemLimit=%d MB, MaxFDs=%d",
		runtime.NumCPU(), memLimit/1024/1024, rLimit.Cur)
}

// getMemoryLimit returns the memory limit in bytes
func getMemoryLimit() uint64 {
	// In production, this would come from cgroup limits or config
	// For now, use 32GB as default
	return 32 * 1024 * 1024 * 1024
}

// getShardID returns the shard ID for this instance
func getShardID() int {
	// In production, this would come from environment or config
	shardIDStr := os.Getenv("SHARD_ID")
	if shardIDStr == "" {
		return 0
	}
	var shardID int
	fmt.Sscanf(shardIDStr, "%d", &shardID)
	return shardID
}

// startMetricsServer starts the Prometheus metrics server
func startMetricsServer() {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"healthy","service":"hot-tier"}`)
	})
	
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "# HELP hot_tier_up Hot-tier service up\n")
		fmt.Fprintf(w, "# TYPE hot_tier_up gauge\n")
		fmt.Fprintf(w, "hot_tier_up 1\n")
	})
	
	log.Info("Metrics server started on :8090")
	if err := http.ListenAndServe(":8090", nil); err != nil {
		log.Errorf("Metrics server failed: %v", err)
	}
}