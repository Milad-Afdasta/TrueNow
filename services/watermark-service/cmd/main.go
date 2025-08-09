package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/aggregator"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/api"
	grpcserver "github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/grpc"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/store"
)

type Config struct {
	Port               int
	GRPCPort           int
	LogLevel           string
	CollectionInterval time.Duration
	CleanupInterval    time.Duration
	ControlPlaneURL    string
	EnableDiscovery    bool
	EnableGRPC         bool
}

func main() {
	// Parse configuration
	config := parseConfig()
	
	// Setup logging
	logger := setupLogger(config.LogLevel)
	
	logger.WithFields(logrus.Fields{
		"port":                config.Port,
		"collection_interval": config.CollectionInterval,
		"cleanup_interval":    config.CleanupInterval,
	}).Info("Starting watermark service")
	
	// Create components
	watermarkStore := store.NewWatermarkStore()
	watermarkAggregator := aggregator.NewWatermarkAggregator(watermarkStore, logger)
	handler := api.NewHandler(watermarkStore, watermarkAggregator, logger)
	
	// Discover service endpoints
	if config.EnableDiscovery {
		if err := watermarkAggregator.DiscoverEndpoints(config.ControlPlaneURL); err != nil {
			logger.WithError(err).Error("Failed to discover endpoints")
		}
	} else {
		// Add some default endpoints for testing
		setupTestEndpoints(watermarkAggregator)
	}
	
	// Set collection interval
	watermarkAggregator.SetCollectionInterval(config.CollectionInterval)
	
	// Start aggregator
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	if err := watermarkAggregator.Start(ctx); err != nil {
		logger.WithError(err).Fatal("Failed to start aggregator")
	}
	
	// Setup HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", config.Port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	
	// Start servers in goroutines
	serverErrors := make(chan error, 2)
	
	// Start HTTP server
	go func() {
		logger.WithField("addr", server.Addr).Info("Starting HTTP server")
		serverErrors <- server.ListenAndServe()
	}()
	
	// Start gRPC server if enabled
	if config.EnableGRPC {
		grpcSrv := grpcserver.NewServer(watermarkStore, watermarkAggregator, logger, config.GRPCPort)
		go func() {
			logger.WithField("port", config.GRPCPort).Info("Starting gRPC server")
			if err := grpcSrv.Start(); err != nil {
				serverErrors <- err
			}
		}()
	}
	
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	// Wait for shutdown signal or server error
	select {
	case err := <-serverErrors:
		if err != http.ErrServerClosed {
			logger.WithError(err).Fatal("Server error")
		}
		
	case sig := <-sigChan:
		logger.WithField("signal", sig).Info("Received shutdown signal")
		
		// Graceful shutdown with timeout
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		
		// Stop aggregator
		watermarkAggregator.Stop()
		
		// Shutdown HTTP server
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.WithError(err).Error("Failed to shutdown server gracefully")
		}
	}
	
	logger.Info("Watermark service stopped")
}

func parseConfig() *Config {
	config := &Config{}
	
	flag.IntVar(&config.Port, "port", 8084, "HTTP server port")
	flag.IntVar(&config.GRPCPort, "grpc-port", 9084, "gRPC server port")
	flag.StringVar(&config.LogLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.DurationVar(&config.CollectionInterval, "collection-interval", 5*time.Second, "Watermark collection interval")
	flag.DurationVar(&config.CleanupInterval, "cleanup-interval", 5*time.Minute, "Stale data cleanup interval")
	flag.StringVar(&config.ControlPlaneURL, "control-plane", "http://localhost:8080", "Control plane URL")
	flag.BoolVar(&config.EnableDiscovery, "enable-discovery", false, "Enable service discovery")
	flag.BoolVar(&config.EnableGRPC, "enable-grpc", true, "Enable gRPC server")
	
	flag.Parse()
	
	// Override from environment variables
	if port := os.Getenv("WATERMARK_PORT"); port != "" {
		fmt.Sscanf(port, "%d", &config.Port)
	}
	if logLevel := os.Getenv("WATERMARK_LOG_LEVEL"); logLevel != "" {
		config.LogLevel = logLevel
	}
	if interval := os.Getenv("WATERMARK_COLLECTION_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			config.CollectionInterval = d
		}
	}
	if url := os.Getenv("CONTROL_PLANE_URL"); url != "" {
		config.ControlPlaneURL = url
	}
	
	return config
}

func setupLogger(level string) *logrus.Logger {
	logger := logrus.New()
	
	// Set format
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
	})
	
	// Set level
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logLevel = logrus.InfoLevel
	}
	logger.SetLevel(logLevel)
	
	return logger
}

func setupTestEndpoints(agg *aggregator.WatermarkAggregator) {
	// Add test endpoints for local development
	// These would normally come from service discovery
	
	// Gateway endpoint
	agg.AddEndpoint(aggregator.ServiceEndpoint{
		ServiceID:   "gateway-local",
		ServiceType: "gateway",
		URL:         "http://localhost:8088",
		Namespace:   "default",
		Table:       "events",
		ShardID:     0,
	})
	
	// Hot tier endpoint
	agg.AddEndpoint(aggregator.ServiceEndpoint{
		ServiceID:   "hot-tier-local",
		ServiceType: "hot-tier",
		URL:         "http://localhost:9090",
		Namespace:   "default",
		Table:       "events",
		ShardID:     0,
	})
	
	// Stream ingester endpoint
	agg.AddEndpoint(aggregator.ServiceEndpoint{
		ServiceID:   "stream-ingester-local",
		ServiceType: "stream-ingester",
		URL:         "http://localhost:8100",
		Namespace:   "default",
		Table:       "events",
		ShardID:     0,
	})
}