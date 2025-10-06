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
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/metrics"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/producer"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/ratelimit"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/validator"
	"github.com/shirou/gopsutil/v4/process"
	log "github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
)

func main() {
	// Parse flags
	backpressure := flag.Bool("backpressure", false, "Enable backpressure management")
	flag.Parse()

	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	metricsExporter := metrics.NewPrometheusExporter("gateway", "ingest-gateway", hostname)

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
	kafkaProducer := producer.NewBatchProducer(kafkaBrokers, metricsExporter)

	// Create ingestion handler
	var (
		server          *fasthttp.Server
		enhancedHandler *ingestion.EnhancedHandler
	)

	if *backpressure {
		log.Info("Starting gateway with backpressure management enabled")
		enhancedHandler = ingestion.NewEnhancedHandler(schemaValidator, kafkaProducer, rateLimiter, metricsExporter)
		server = &fasthttp.Server{
			Handler:                      enhancedHandler.HandleWithBackpressure,
			Name:                         "flow-gateway",
			Concurrency:                  256 * 1024, // Support 256K concurrent connections
			DisableKeepalive:             false,
			TCPKeepalive:                 true,
			TCPKeepalivePeriod:           60 * time.Second,
			MaxRequestBodySize:           10 * 1024 * 1024, // 10MB max
			ReadBufferSize:               64 * 1024,        // 64KB read buffer
			WriteBufferSize:              64 * 1024,        // 64KB write buffer
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
			NoDefaultServerHeader: true,
			NoDefaultDate:         true,
			NoDefaultContentType:  true,
			ReduceMemoryUsage:     false, // Keep false for performance
		}
	} else {
		log.Info("Starting gateway without backpressure management")
		handler := ingestion.NewHandler(schemaValidator, kafkaProducer, rateLimiter, metricsExporter)
		server = &fasthttp.Server{
			Handler:                      handler.Handle,
			Name:                         "flow-gateway",
			Concurrency:                  256 * 1024, // Support 256K concurrent connections
			DisableKeepalive:             false,
			TCPKeepalive:                 true,
			TCPKeepalivePeriod:           60 * time.Second,
			MaxRequestBodySize:           10 * 1024 * 1024, // 10MB max
			ReadBufferSize:               64 * 1024,        // 64KB read buffer
			WriteBufferSize:              64 * 1024,        // 64KB write buffer
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
			NoDefaultServerHeader: true,
			NoDefaultDate:         true,
			NoDefaultContentType:  true,
			ReduceMemoryUsage:     false, // Keep false for performance
		}
	}

	startRuntimeCollectors(metricsExporter, enhancedHandler)

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

func startRuntimeCollectors(metricsExporter *metrics.PrometheusExporter, enhanced *ingestion.EnhancedHandler) {
	if metricsExporter == nil {
		return
	}

	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		log.WithError(err).Warn("gateway: unable to initialize process metrics collector")
		return
	}

	// Warm up CPU sampling to avoid first-call zero values.
	if _, err := proc.Percent(0); err != nil {
		log.WithError(err).Debug("gateway: initial CPU percent sample failed")
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			cpuPercent, err := proc.Percent(0)
			if err != nil {
				log.WithError(err).Debug("gateway: CPU percent sampling failed")
				continue
			}

			memPercent, err := proc.MemoryPercent()
			if err != nil {
				log.WithError(err).Debug("gateway: memory percent sampling failed")
				continue
			}

			fds, err := proc.NumFDs()
			if err != nil {
				log.WithError(err).Debug("gateway: file descriptor sampling failed")
				fds = 0
			}

			metricsExporter.RecordSystemMetrics(
				cpuPercent,
				float64(memPercent),
				int32(runtime.NumGoroutine()),
				int64(fds),
			)

			if enhanced != nil {
				queueMetrics := enhanced.GetQueueMetrics()
				enhanced.PushSystemMetrics(ingestion.SystemMetrics{
					CPUPercent:    cpuPercent,
					MemoryPercent: float64(memPercent),
					QueueDepth:    queueMetrics.Queued,
				})
			}
		}
	}()
}
