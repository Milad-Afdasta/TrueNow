package main

import (
	"context"
	"flag"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/ingestion"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/producer"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/ratelimit"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/validator"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
	log "github.com/sirupsen/logrus"
)

func main() {
	// Parse flags
	backpressure := flag.Bool("backpressure", false, "Enable backpressure management")
	flag.Parse()

	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	// Pin to CPU cores for NUMA optimization
	runtime.GOMAXPROCS(runtime.NumCPU())
	
	// Initialize components
	rateLimiter := ratelimit.NewHierarchicalLimiter(1000000) // 1M QPS base
	schemaValidator := validator.NewSIMDValidator()
	// Connect to Kafka/Redpanda
	kafkaBrokers := []string{"localhost:19092"}
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaBrokers = []string{brokers}
	}
	kafkaProducer := producer.NewBatchProducer(kafkaBrokers)
	
	// Create ingestion handler
	var server *fasthttp.Server
	
	if *backpressure {
		log.Info("Starting gateway with backpressure management enabled")
		enhancedHandler := ingestion.NewEnhancedHandler(schemaValidator, kafkaProducer, rateLimiter)
		server = &fasthttp.Server{
			Handler:                       enhancedHandler.HandleWithBackpressure,
			Name:                         "flow-gateway",
			Concurrency:                  256 * 1024,  // Support 256K concurrent connections
			DisableKeepalive:             false,
			TCPKeepalive:                 true,
			TCPKeepalivePeriod:          60 * time.Second,
			MaxRequestBodySize:           10 * 1024 * 1024, // 10MB max
			ReadBufferSize:               64 * 1024,         // 64KB read buffer
			WriteBufferSize:              64 * 1024,         // 64KB write buffer
			ReadTimeout:                  5 * time.Second,
			WriteTimeout:                 5 * time.Second,
			IdleTimeout:                  60 * time.Second,
			MaxConnsPerIP:                10000,
			MaxRequestsPerConn:           10000,
			MaxKeepaliveDuration:         5 * time.Minute,
			GetOnly:                      false,
			DisablePreParseMultipartForm: true,
			LogAllErrors:                 false,
			SecureErrorLogMessage:        true,
			StreamRequestBody:            true, // Stream large requests
			
			// Performance optimizations
			NoDefaultServerHeader:        true,
			NoDefaultDate:               true,
			NoDefaultContentType:        true,
			ReduceMemoryUsage:           false, // Keep false for performance
		}
	} else {
		log.Info("Starting gateway without backpressure management")
		handler := ingestion.NewHandler(schemaValidator, kafkaProducer, rateLimiter)
		server = &fasthttp.Server{
			Handler:                       handler.Handle,
			Name:                         "flow-gateway",
			Concurrency:                  256 * 1024,  // Support 256K concurrent connections
			DisableKeepalive:             false,
			TCPKeepalive:                 true,
			TCPKeepalivePeriod:          60 * time.Second,
			MaxRequestBodySize:           10 * 1024 * 1024, // 10MB max
			ReadBufferSize:               64 * 1024,         // 64KB read buffer
			WriteBufferSize:              64 * 1024,         // 64KB write buffer
			ReadTimeout:                  5 * time.Second,
			WriteTimeout:                 5 * time.Second,
			IdleTimeout:                  60 * time.Second,
			MaxConnsPerIP:                10000,
			MaxRequestsPerConn:           10000,
			MaxKeepaliveDuration:         5 * time.Minute,
			GetOnly:                      false,
			DisablePreParseMultipartForm: true,
			LogAllErrors:                 false,
			SecureErrorLogMessage:        true,
			StreamRequestBody:            true, // Stream large requests
			
			// Performance optimizations
			NoDefaultServerHeader:        true,
			NoDefaultDate:               true,
			NoDefaultContentType:        true,
			ReduceMemoryUsage:           false, // Keep false for performance
		}
	}

	// Use SO_REUSEPORT for better multi-core scaling
	ln, err := reuseport.Listen("tcp4", ":8088")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Enable TCP optimizations
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		rawConn, err := tcpLn.SyscallConn()
		if err == nil {
			rawConn.Control(func(fd uintptr) {
				// Enable TCP_NODELAY for low latency
				syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
				// Increase socket buffers
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4*1024*1024)
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 4*1024*1024)
			})
		}
	}

	// Start server in goroutine
	go func() {
		log.Infof("Gateway starting on :8088 with %d CPU cores", runtime.NumCPU())
		if err := server.Serve(ln); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Graceful shutdown
	log.Info("Shutting down gateway...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.ShutdownWithContext(ctx); err != nil {
		log.Errorf("Gateway forced to shutdown: %v", err)
	}

	log.Info("Gateway exited")
}