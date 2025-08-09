package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/metrics"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

func main() {
	fmt.Println("🔍 Testing Metrics Collection")
	fmt.Println("=" + string(make([]byte, 50)))
	
	// Configure collector
	config := metrics.DefaultCollectorConfig()
	config.Endpoint = "http://localhost:9090"
	config.CacheEnabled = true
	config.CacheTTL = 15 * time.Second
	
	// Determine which collector to use
	useMock := os.Getenv("USE_MOCK") == "true"
	
	var collector metrics.Collector
	if useMock {
		fmt.Println("📊 Using Mock Collector (for testing)")
		collector = metrics.NewMockCollector(config)
	} else {
		fmt.Println("📊 Using Prometheus Collector (simulation mode if Prometheus not running)")
		collector = metrics.NewPrometheusCollector(config)
	}
	
	ctx := context.Background()
	
	// Check collector health
	if collector.IsHealthy(ctx) {
		fmt.Println("✅ Collector is healthy")
	} else {
		fmt.Println("⚠️  Collector is unhealthy")
	}
	
	// Define test instances for each service
	services := map[string][]*types.Instance{
		"gateway": {
			{
				ID:          "gateway-1",
				ServiceName: "gateway",
				Host:        "localhost",
				Port:        8088,
				Status:      types.StatusRunning,
				StartTime:   time.Now().Add(-1 * time.Hour),
			},
			{
				ID:          "gateway-2",
				ServiceName: "gateway",
				Host:        "localhost",
				Port:        8089,
				Status:      types.StatusRunning,
				StartTime:   time.Now().Add(-30 * time.Minute),
			},
		},
		"processor": {
			{
				ID:          "processor-1",
				ServiceName: "processor",
				Host:        "localhost",
				Port:        8091,
				Status:      types.StatusRunning,
				StartTime:   time.Now().Add(-45 * time.Minute),
			},
		},
		"aggregator": {
			{
				ID:          "aggregator-1",
				ServiceName: "aggregator",
				Host:        "localhost",
				Port:        8092,
				Status:      types.StatusRunning,
				StartTime:   time.Now().Add(-20 * time.Minute),
			},
		},
		"hot-tier": {
			{
				ID:          "hot-tier-1",
				ServiceName: "hot-tier",
				Host:        "localhost",
				Port:        8090,
				Status:      types.StatusRunning,
				StartTime:   time.Now().Add(-2 * time.Hour),
			},
		},
	}
	
	// Collect metrics for each service
	fmt.Println("\n📈 Service Metrics:")
	fmt.Println("-" + string(make([]byte, 50)))
	
	for serviceName, instances := range services {
		serviceMetrics, err := collector.CollectServiceMetrics(ctx, serviceName, instances)
		if err != nil {
			log.Printf("❌ Failed to collect metrics for %s: %v", serviceName, err)
			continue
		}
		
		fmt.Printf("\n🔧 Service: %s\n", serviceName)
		fmt.Printf("   Instances: %d\n", serviceMetrics.Instances)
		fmt.Printf("   Timestamp: %s\n", serviceMetrics.Timestamp.Format("15:04:05"))
		fmt.Println("   Metrics:")
		
		for metricName, metricValue := range serviceMetrics.Metrics {
			unit := metricValue.Unit
			if unit == "" {
				unit = "units"
			}
			fmt.Printf("   ├─ %-15s: %.2f %s", metricName, metricValue.Value, unit)
			if metricValue.Aggregation != "" {
				fmt.Printf(" (%s", metricValue.Aggregation)
				if metricValue.Window > 0 {
					fmt.Printf(" over %v", metricValue.Window)
				}
				fmt.Printf(")")
			}
			fmt.Println()
		}
	}
	
	// Collect instance-level metrics
	fmt.Println("\n📊 Instance Metrics:")
	fmt.Println("-" + string(make([]byte, 50)))
	
	for serviceName, instances := range services {
		for _, instance := range instances {
			instanceMetrics, err := collector.CollectInstanceMetrics(ctx, instance)
			if err != nil {
				log.Printf("❌ Failed to collect metrics for instance %s: %v", instance.ID, err)
				continue
			}
			
			fmt.Printf("\n📍 Instance: %s (%s)\n", instance.ID, serviceName)
			fmt.Printf("   Host: %s:%d\n", instance.Host, instance.Port)
			fmt.Printf("   Healthy: %v\n", instanceMetrics.Healthy)
			fmt.Printf("   Uptime: %v\n", time.Since(instance.StartTime).Round(time.Minute))
			fmt.Println("   Metrics:")
			
			for metricName, metricValue := range instanceMetrics.Metrics {
				unit := metricValue.Unit
				if unit == "" {
					unit = "units"
				}
				fmt.Printf("   ├─ %-15s: %.2f %s\n", metricName, metricValue.Value, unit)
			}
		}
	}
	
	// Test custom query
	fmt.Println("\n🔬 Custom Metric Query:")
	fmt.Println("-" + string(make([]byte, 50)))
	
	customQuery := `sum(rate(http_requests_total[5m]))`
	value, err := collector.QueryCustomMetric(ctx, customQuery, 5*time.Minute)
	if err != nil {
		log.Printf("❌ Failed to execute custom query: %v", err)
	} else {
		fmt.Printf("Query: %s\n", customQuery)
		fmt.Printf("Result: %.2f\n", value)
	}
	
	// Test caching
	fmt.Println("\n🔄 Testing Cache:")
	fmt.Println("-" + string(make([]byte, 50)))
	
	start := time.Now()
	value1, _ := collector.QueryCustomMetric(ctx, "test_query", time.Minute)
	duration1 := time.Since(start)
	
	start = time.Now()
	value2, _ := collector.QueryCustomMetric(ctx, "test_query", time.Minute)
	duration2 := time.Since(start)
	
	fmt.Printf("First query:  %.2f (took %v)\n", value1, duration1)
	fmt.Printf("Second query: %.2f (took %v)\n", value2, duration2)
	
	if duration2 < duration1/2 {
		fmt.Println("✅ Cache is working (second query was faster)")
	} else {
		fmt.Println("ℹ️  Cache performance not significant (might be using simulation)")
	}
	
	// Continuous monitoring mode
	if os.Getenv("MONITOR") == "true" {
		fmt.Println("\n📡 Continuous Monitoring Mode (press Ctrl+C to stop)")
		fmt.Println("-" + string(make([]byte, 50)))
		
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				fmt.Printf("\n[%s] Collecting metrics...\n", time.Now().Format("15:04:05"))
				
				for serviceName, instances := range services {
					serviceMetrics, err := collector.CollectServiceMetrics(ctx, serviceName, instances)
					if err != nil {
						continue
					}
					
					// Show key metrics only
					cpu := serviceMetrics.Metrics["cpu"].Value
					mem := serviceMetrics.Metrics["memory"].Value
					
					fmt.Printf("  %s: CPU=%.1f%%, Memory=%.1f", serviceName, cpu, mem)
					
					if serviceName == "gateway" {
						if reqRate, ok := serviceMetrics.Metrics["request_rate"]; ok {
							fmt.Printf(", Requests=%.0f/s", reqRate.Value)
						}
					} else if serviceName == "processor" || serviceName == "aggregator" {
						if lag, ok := serviceMetrics.Metrics["kafka_lag"]; ok {
							fmt.Printf(", Lag=%.0f", lag.Value)
						}
					}
					fmt.Println()
				}
			}
		}
	}
	
	// Clean up
	if err := collector.Close(); err != nil {
		log.Printf("Warning: Failed to close collector: %v", err)
	}
	
	fmt.Println("\n✅ Metrics collection test completed!")
}