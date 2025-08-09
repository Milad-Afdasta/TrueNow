package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/autoscaler"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/config"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

func createMinimalConfig() *types.AutoScalerConfig {
	return &types.AutoScalerConfig{
		EvaluationInterval: 3 * time.Second,
		MetricsWindow:      5 * time.Minute,
		EnableCascading:    true,
		EnableBackpressure: true,
		UseMockProcesses:   true,
		Services: map[string]*types.ServiceConfig{
			"gateway": {
				Name:         "gateway",
				MinInstances: 1,
				MaxInstances: 6,
				StartCommand: "echo gateway",
				Metrics: []types.MetricConfig{
					{Type: types.MetricCPU, Threshold: 50, Aggregation: types.AggregationAvg},
					{Type: types.MetricMemory, Threshold: 70, Aggregation: types.AggregationMax},
				},
				ScaleOut: types.ScalingPolicy{Increment: 1, Cooldown: 30 * time.Second},
				ScaleIn:  types.ScalingPolicy{Increment: 1, Cooldown: 60 * time.Second},
			},
			"stream-ingester": {
				Name:         "stream-ingester",
				MinInstances: 1,
				MaxInstances: 4,
				StartCommand: "echo stream-ingester",
				Metrics: []types.MetricConfig{
					{Type: types.MetricCPU, Threshold: 50, Aggregation: types.AggregationAvg},
					{Type: types.MetricMemory, Threshold: 70, Aggregation: types.AggregationMax},
				},
				ScaleOut: types.ScalingPolicy{Increment: 1, Cooldown: 30 * time.Second},
				ScaleIn:  types.ScalingPolicy{Increment: 1, Cooldown: 60 * time.Second},
			},
			"hot-tier": {
				Name:         "hot-tier",
				MinInstances: 1,
				MaxInstances: 3,
				StartCommand: "echo hot-tier",
				Metrics: []types.MetricConfig{
					{Type: types.MetricCPU, Threshold: 50, Aggregation: types.AggregationAvg},
					{Type: types.MetricMemory, Threshold: 70, Aggregation: types.AggregationMax},
				},
				ScaleOut: types.ScalingPolicy{Increment: 1, Cooldown: 30 * time.Second},
				ScaleIn:  types.ScalingPolicy{Increment: 1, Cooldown: 60 * time.Second},
			},
			"query-api": {
				Name:         "query-api",
				MinInstances: 1,
				MaxInstances: 2,
				StartCommand: "echo query-api",
				Metrics: []types.MetricConfig{
					{Type: types.MetricCPU, Threshold: 50, Aggregation: types.AggregationAvg},
					{Type: types.MetricMemory, Threshold: 70, Aggregation: types.AggregationMax},
				},
				ScaleOut: types.ScalingPolicy{Increment: 1, Cooldown: 30 * time.Second},
				ScaleIn:  types.ScalingPolicy{Increment: 1, Cooldown: 60 * time.Second},
			},
		},
		Prometheus: types.PrometheusConfig{
			URL:     "http://localhost:9090",
			Timeout: 10 * time.Second,
		},
	}
}

func main() {
	var (
		configPath = flag.String("config", "configs/autoscaler.yaml", "Path to configuration file")
		dryRun     = flag.Bool("dry-run", false, "Run in dry-run mode (no actual scaling)")
		status     = flag.Bool("status", false, "Show current status and exit")
	)
	flag.Parse()
	
	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		// If in dry-run mode and config load fails, create a minimal config
		if *dryRun {
			cfg = createMinimalConfig()
			cfg.UseMockProcesses = true
			log.Println("Running in dry-run mode with mock processes and minimal config")
		} else {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		// Override for dry-run mode
		if *dryRun {
			cfg.UseMockProcesses = true
			log.Println("Running in dry-run mode with mock processes")
		}
	}
	
	// Create autoscaler
	scaler, err := autoscaler.NewAutoScaler(cfg)
	if err != nil {
		log.Fatalf("Failed to create autoscaler: %v", err)
	}
	
	ctx := context.Background()
	
	// Show status if requested
	if *status {
		showStatus(ctx, scaler)
		return
	}
	
	// Start autoscaler
	log.Printf("Starting autoscaler with evaluation interval: %v", cfg.EvaluationInterval)
	if err := scaler.Start(ctx); err != nil {
		log.Fatalf("Failed to start autoscaler: %v", err)
	}
	
	// Start HTTP API server for monitoring
	go startAPIServer(scaler)
	
	// Print initial status
	time.Sleep(3 * time.Second) // Wait for initial instances to start
	showStatus(ctx, scaler)
	
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	// Status ticker
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()
	
	// Wait for shutdown signal
	for {
		select {
		case <-sigChan:
			log.Println("Received shutdown signal, stopping autoscaler...")
			stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := scaler.Stop(stopCtx); err != nil {
				log.Printf("Error stopping autoscaler: %v", err)
			}
			cancel()
			return
			
		case <-statusTicker.C:
			// Periodic status update
			fmt.Println("\n" + strings.Repeat("=", 80))
			showStatus(ctx, scaler)
		}
	}
}

func showStatus(ctx context.Context, scaler *autoscaler.AutoScaler) {
	status, err := scaler.GetStatus(ctx)
	if err != nil {
		log.Printf("Failed to get status: %v", err)
		return
	}
	
	fmt.Printf("\nAutoScaler Status: %s\n", formatRunning(status.Running))
	fmt.Println(strings.Repeat("-", 80))
	
	if len(status.Services) == 0 {
		fmt.Println("No services configured")
		return
	}
	
	// Print service status table
	fmt.Printf("%-20s %-15s %-15s %-15s %-15s\n", 
		"Service", "Running", "Total", "Min", "Max")
	fmt.Println(strings.Repeat("-", 80))
	
	for _, svcStatus := range status.Services {
		fmt.Printf("%-20s %-15s %-15s %-15d %-15d\n",
			svcStatus.ServiceName,
			formatCount(svcStatus.RunningCount),
			formatCount(svcStatus.TotalCount),
			svcStatus.MinInstances,
			svcStatus.MaxInstances)
		
		// Show instance details
		if len(svcStatus.Instances) > 0 {
			for _, inst := range svcStatus.Instances {
				fmt.Printf("  └─ %s: %s (port %d, pid %d)\n",
					inst.ID[:20]+"...",
					inst.Status,
					inst.Port,
					inst.PID)
			}
		}
	}
	
	// Check for max scale
	atMax, maxedServices, err := scaler.IsAtMaxScale(ctx)
	if err == nil && atMax {
		fmt.Printf("\n⚠️  Services at max scale: %v\n", maxedServices)
		fmt.Println("Backpressure should be activated for these services")
	}
	
	// Show backpressure status
	if bpStatus := scaler.GetBackpressureStatus(); bpStatus != nil && bpStatus.Enabled {
		fmt.Printf("\n🔴 Backpressure Status:\n")
		fmt.Printf("   Current Level: %s\n", bpStatus.CurrentLevel)
		fmt.Printf("   Level Changes: %d\n", bpStatus.LevelChanges)
		fmt.Printf("   Requests Dropped: %d\n", bpStatus.RequestsDropped)
		
		if bpStatus.Strategy.RateLimitReduction > 0 {
			fmt.Printf("   Rate Limit Reduction: %.0f%%\n", bpStatus.Strategy.RateLimitReduction)
		}
		if bpStatus.Strategy.RejectPercentage > 0 {
			fmt.Printf("   Request Rejection: %.0f%%\n", bpStatus.Strategy.RejectPercentage)
		}
		if bpStatus.Strategy.CircuitBreaker {
			fmt.Printf("   Circuit Breaker: ACTIVE\n")
		}
	}
}

func formatRunning(running bool) string {
	if running {
		return "✅ Running"
	}
	return "❌ Stopped"
}

func formatCount(count int) string {
	return fmt.Sprintf("%d", count)
}

// startAPIServer starts an HTTP server for monitoring
func startAPIServer(scaler *autoscaler.AutoScaler) {
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		status, err := scaler.GetStatus(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		// Convert to a format the monitor expects
		response := map[string]interface{}{
			"running": status.Running,
			"services": make(map[string]interface{}),
		}
		
		for serviceName, svcStatus := range status.Services {
			instances := make([]map[string]interface{}, 0)
			for _, inst := range svcStatus.Instances {
				instances = append(instances, map[string]interface{}{
					"id":       inst.ID,
					"service":  inst.ServiceName,
					"status":   string(inst.Status),
					"port":     inst.Port,
					"pid":      inst.PID,
					"health":   inst.IsHealthy(),
					"uptime":   time.Since(inst.StartTime).Seconds(),
				})
			}
			
			response["services"].(map[string]interface{})[serviceName] = map[string]interface{}{
				"name":          serviceName,
				"running_count": svcStatus.RunningCount,
				"total_count":   svcStatus.TotalCount,
				"min_instances": svcStatus.MinInstances,
				"max_instances": svcStatus.MaxInstances,
				"instances":     instances,
			}
		}
		
		// Add backpressure status
		if bpStatus := scaler.GetBackpressureStatus(); bpStatus != nil && bpStatus.Enabled {
			response["backpressure"] = map[string]interface{}{
				"enabled":          true,
				"level":            bpStatus.CurrentLevel,
				"requests_dropped": bpStatus.RequestsDropped,
			}
		}
		
		// Check for max scale
		atMax, maxedServices, _ := scaler.IsAtMaxScale(ctx)
		if atMax {
			response["at_max_scale"] = true
			response["maxed_services"] = maxedServices
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})
	
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})
	
	port := ":8095"  // Changed to 8095 to avoid conflicts with Docker
	log.Printf("Starting API server on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Printf("API server error: %v", err)
	}
}