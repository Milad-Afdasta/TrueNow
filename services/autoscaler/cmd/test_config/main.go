package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/config"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: test_config <config-file>")
		os.Exit(1)
	}
	
	configPath := os.Args[1]
	fmt.Printf("Loading configuration from: %s\n\n", configPath)
	
	loader := config.NewLoader(configPath)
	cfg, err := loader.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	
	fmt.Println("✅ Configuration loaded and validated successfully!")
	fmt.Printf("\n📊 Configuration Summary:\n")
	fmt.Printf("   Services configured: %d\n", len(cfg.Services))
	fmt.Printf("   Metrics endpoint: %s\n", cfg.MetricsEndpoint)
	fmt.Printf("   Evaluation interval: %v\n", cfg.EvaluationInterval)
	fmt.Printf("   Cascading enabled: %v\n", cfg.EnableCascading)
	fmt.Printf("   Backpressure enabled: %v\n", cfg.EnableBackpressure)
	fmt.Printf("   Log level: %s\n", cfg.LogLevel)
	
	fmt.Printf("\n🔧 Service Details:\n")
	for name, svc := range cfg.Services {
		fmt.Printf("\n   Service: %s\n", name)
		fmt.Printf("   ├─ Instances: %d (min) - %d (max)\n", svc.MinInstances, svc.MaxInstances)
		fmt.Printf("   ├─ Stateful: %v\n", svc.Stateful)
		fmt.Printf("   ├─ Work Dir: %s\n", svc.WorkDir)
		fmt.Printf("   ├─ Start Command: %s\n", svc.StartCommand)
		fmt.Printf("   ├─ Stop Timeout: %v\n", svc.StopTimeout)
		fmt.Printf("   ├─ Metrics:\n")
		for _, m := range svc.Metrics {
			fmt.Printf("   │  ├─ %s: threshold=%.1f, aggregation=%s, window=%v\n", 
				m.Type, m.Threshold, m.Aggregation, m.Window)
		}
		fmt.Printf("   ├─ Scale Out: increment=%d, cooldown=%v, strategy=%s\n", 
			svc.ScaleOut.Increment, svc.ScaleOut.Cooldown, svc.ScaleOut.Strategy)
		fmt.Printf("   ├─ Scale In: increment=%d, cooldown=%v, strategy=%s\n", 
			svc.ScaleIn.Increment, svc.ScaleIn.Cooldown, svc.ScaleIn.Strategy)
		fmt.Printf("   └─ Health Check: endpoint=%s, interval=%v, timeout=%v, retries=%d\n",
			svc.HealthCheck.Endpoint, svc.HealthCheck.Interval, svc.HealthCheck.Timeout, svc.HealthCheck.Retries)
	}
	
	// Output JSON for debugging
	if os.Getenv("DEBUG") == "true" {
		fmt.Printf("\n📝 Full Configuration (JSON):\n")
		jsonData, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(jsonData))
	}
}