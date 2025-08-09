package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ANSI color codes
const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Bold    = "\033[1m"
	Clear   = "\033[2J\033[H"
)

type ServiceStatus struct {
	Name      string
	Port      int
	Instances int
	Status    string
	Health    bool
	Stats     map[string]interface{}
}

type KafkaStats struct {
	Topics     int
	Partitions int
	Messages   int64
	Lag        int64
	Brokers    int
}

type SystemMetrics struct {
	// Service status
	Services map[string]*ServiceStatus

	// Kafka metrics
	Kafka KafkaStats

	// Performance metrics
	EventsIngested   atomic.Int64
	EventsProcessed  atomic.Int64
	QueryCount       atomic.Int64
	AvgLatency       atomic.Int64
	P99Latency       atomic.Int64
	ErrorRate        atomic.Int64

	// Resource usage
	CPUUsage    float64
	MemoryUsage float64
	DiskIO      float64
	NetworkIO   float64

	// Load test metrics
	LoadTestActive bool
	LoadTestRate   int
	LoadTestSent   atomic.Int64
	LoadTestFailed atomic.Int64
}

func main() {
	var (
		interval = flag.Duration("interval", 1*time.Second, "Update interval")
		loadTest = flag.Bool("load", false, "Run load test")
		rate     = flag.Int("rate", 100, "Load test rate (events/sec)")
	)
	flag.Parse()

	metrics := &SystemMetrics{
		Services: make(map[string]*ServiceStatus),
	}

	// Initialize services
	initializeServices(metrics)

	// Run initial health checks
	client := &http.Client{Timeout: 1 * time.Second}
	checkAllServices(metrics, client)

	// Start monitoring goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Monitor services
	wg.Add(1)
	go monitorServices(ctx, metrics, &wg)

	// Monitor Kafka
	wg.Add(1)
	go monitorKafka(ctx, metrics, &wg)

	// Monitor system resources
	wg.Add(1)
	go monitorResources(ctx, metrics, &wg)

	// Start load test if requested
	if *loadTest {
		metrics.LoadTestActive = true
		metrics.LoadTestRate = *rate
		wg.Add(1)
		go runLoadTest(ctx, metrics, *rate, &wg)
	}

	// Display loop
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			displayDashboard(metrics)
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
}

func initializeServices(metrics *SystemMetrics) {
	metrics.Services["PostgreSQL"] = &ServiceStatus{
		Name: "PostgreSQL", Port: 5432, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Redis"] = &ServiceStatus{
		Name: "Redis", Port: 6379, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Redpanda"] = &ServiceStatus{
		Name: "Redpanda", Port: 19092, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Control-Plane"] = &ServiceStatus{
		Name: "Control-Plane", Port: 8080, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Gateway"] = &ServiceStatus{
		Name: "Gateway", Port: 8088, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Hot-Tier"] = &ServiceStatus{
		Name: "Hot-Tier", Port: 9090, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Query-API"] = &ServiceStatus{
		Name: "Query-API", Port: 8081, Instances: 1, Status: "Unknown",
	}
	metrics.Services["Stream-Ingester"] = &ServiceStatus{
		Name: "Stream-Ingester", Port: 0, Instances: 1, Status: "Unknown",
	}
}

func checkAllServices(metrics *SystemMetrics, client *http.Client) {
	for name, service := range metrics.Services {
		switch name {
		case "PostgreSQL":
			checkPostgres(service)
		case "Redis":
			checkRedis(service)
		case "Redpanda":
			checkRedpanda(service)
		case "Control-Plane", "Gateway", "Query-API":
			checkHTTPService(client, service)
		case "Hot-Tier":
			checkGRPCService(service)
		case "Stream-Ingester":
			checkStreamIngester(service)
		}
	}
}

func monitorServices(ctx context.Context, metrics *SystemMetrics, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 1 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkAllServices(metrics, client)
		}
	}
}

func checkHTTPService(client *http.Client, service *ServiceStatus) {
	// Try health endpoint first, then just check if port is open
	url := fmt.Sprintf("http://localhost:%d/health", service.Port)
	resp, err := client.Get(url)
	if err != nil {
		// Fallback to just checking if service is listening
		url = fmt.Sprintf("http://localhost:%d/", service.Port)
		resp, err = client.Get(url)
		if err != nil {
			// Check if port is at least open
			cmd := exec.Command("nc", "-z", "localhost", fmt.Sprintf("%d", service.Port))
			if err := cmd.Run(); err != nil {
				service.Status = "Down"
				service.Health = false
			} else {
				service.Status = "Running"
				service.Health = true
			}
			return
		}
		defer resp.Body.Close()
	}

	if resp != nil {
		defer resp.Body.Close()
		service.Status = "Running"
		service.Health = resp.StatusCode < 500
	}
}

func checkPostgres(service *ServiceStatus) {
	// First try pg_isready
	cmd := exec.Command("pg_isready", "-h", "localhost", "-p", "5432")
	if err := cmd.Run(); err == nil {
		service.Status = "Running"
		service.Health = true
		return
	}
	
	// Fallback to nc check
	cmd = exec.Command("nc", "-z", "localhost", "5432")
	if err := cmd.Run(); err == nil {
		service.Status = "Running"
		service.Health = true
	} else {
		service.Status = "Down"
		service.Health = false
	}
}

func checkRedis(service *ServiceStatus) {
	// First try redis-cli
	cmd := exec.Command("redis-cli", "ping")
	if output, err := cmd.Output(); err == nil && strings.Contains(string(output), "PONG") {
		service.Status = "Running"
		service.Health = true
		return
	}
	
	// Fallback to nc check
	cmd = exec.Command("nc", "-z", "localhost", "6379")
	if err := cmd.Run(); err == nil {
		service.Status = "Running"
		service.Health = true
	} else {
		service.Status = "Down"
		service.Health = false
	}
}

func checkRedpanda(service *ServiceStatus) {
	// Check if container is running
	cmd := exec.Command("docker", "ps", "--filter", "name=redpanda", "--filter", "status=running", "-q")
	if output, err := cmd.Output(); err == nil && len(output) > 0 {
		service.Status = "Running"
		service.Health = true
	} else {
		service.Status = "Down"
		service.Health = false
	}
}

func checkGRPCService(service *ServiceStatus) {
	// Use nc to check if port is open
	cmd := exec.Command("nc", "-z", "localhost", fmt.Sprintf("%d", service.Port))
	if err := cmd.Run(); err == nil {
		service.Status = "Running"
		service.Health = true
	} else {
		service.Status = "Down"
		service.Health = false
	}
}

func checkStreamIngester(service *ServiceStatus) {
	// Check if process is running using ps
	cmd := exec.Command("sh", "-c", "ps aux | grep stream-ingester | grep -v grep")
	if output, err := cmd.Output(); err == nil && len(output) > 0 {
		service.Status = "Running"
		service.Health = true
	} else {
		service.Status = "Down"
		service.Health = false
	}
}

func monitorKafka(ctx context.Context, metrics *SystemMetrics, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get topic list
			cmd := exec.Command("docker", "exec", "redpanda", "rpk", "topic", "list", "--format", "json")
			output, err := cmd.Output()
			if err == nil {
				var topics []map[string]interface{}
				if json.Unmarshal(output, &topics) == nil {
					metrics.Kafka.Topics = len(topics)
					totalPartitions := 0
					for _, topic := range topics {
						if partitions, ok := topic["partitions"].(float64); ok {
							totalPartitions += int(partitions)
						}
					}
					metrics.Kafka.Partitions = totalPartitions
				}
			}

			// Get consumer group lag
			cmd = exec.Command("docker", "exec", "redpanda", "rpk", "group", "describe", "stream-ingester", "--format", "json")
			output, err = cmd.Output()
			if err == nil {
				var group map[string]interface{}
				if json.Unmarshal(output, &group) == nil {
					if lag, ok := group["total_lag"].(float64); ok {
						metrics.Kafka.Lag = int64(lag)
					}
				}
			}
		}
	}
}

func monitorResources(ctx context.Context, metrics *SystemMetrics, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get CPU usage
			cmd := exec.Command("sh", "-c", "ps aux | awk '{sum+=$3} END {print sum}'")
			if output, err := cmd.Output(); err == nil {
				fmt.Sscanf(string(output), "%f", &metrics.CPUUsage)
			}

			// Get memory usage
			cmd = exec.Command("sh", "-c", "ps aux | awk '{sum+=$4} END {print sum}'")
			if output, err := cmd.Output(); err == nil {
				fmt.Sscanf(string(output), "%f", &metrics.MemoryUsage)
			}
		}
	}
}

func runLoadTest(ctx context.Context, metrics *SystemMetrics, rate int, wg *sync.WaitGroup) {
	defer wg.Done()
	
	client := &http.Client{Timeout: 5 * time.Second}
	interval := time.Second / time.Duration(rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			go sendTestEvent(client, metrics)
		}
	}
}

func sendTestEvent(client *http.Client, metrics *SystemMetrics) {
	event := map[string]interface{}{
		"event_id":   fmt.Sprintf("evt_%d", time.Now().UnixNano()),
		"event_time": time.Now().UnixMilli(),
		"revision":   1,
		"dims": map[string]string{
			"user_id": fmt.Sprintf("user_%d", time.Now().Unix()%1000),
			"type":    "test",
		},
		"metrics": map[string]float64{
			"value": float64(time.Now().Unix() % 1000),
		},
	}

	body, _ := json.Marshal(event)
	req, _ := http.NewRequest("POST", "http://localhost:8088/v1/ingest", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Namespace", "analytics")
	req.Header.Set("X-Table", "events")

	metrics.LoadTestSent.Add(1)
	
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		metrics.LoadTestFailed.Add(1)
	} else {
		metrics.EventsIngested.Add(1)
		resp.Body.Close()
	}
}

func displayDashboard(metrics *SystemMetrics) {
	// Clear screen
	fmt.Print(Clear)
	
	// Header
	fmt.Printf("%s%s╔════════════════════════════════════════════════════════════════════════════════════╗%s\n", Bold, Cyan, Reset)
	fmt.Printf("%s%s║                    REAL-TIME ANALYTICS PLATFORM - SYSTEM MONITOR                      ║%s\n", Bold, Cyan, Reset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════════════════════════════╝%s\n", Bold, Cyan, Reset)
	fmt.Printf("  %sTime: %s%s\n\n", Yellow, time.Now().Format("2006-01-02 15:04:05"), Reset)

	// Service Status
	fmt.Printf("%s%s▌ SERVICE STATUS%s\n", Bold, Blue, Reset)
	fmt.Printf("┌─────────────────┬──────────┬───────────┬────────┬──────────────────────────────────┐\n")
	fmt.Printf("│ Service         │ Port     │ Instances │ Status │ Health                           │\n")
	fmt.Printf("├─────────────────┼──────────┼───────────┼────────┼──────────────────────────────────┤\n")
	
	for _, name := range []string{"PostgreSQL", "Redis", "Redpanda", "Control-Plane", "Gateway", "Hot-Tier", "Query-API", "Stream-Ingester"} {
		if service, ok := metrics.Services[name]; ok {
			statusColor := Red
			if service.Status == "Running" {
				statusColor = Green
			}
			healthBar := generateHealthBar(service.Health)
			portStr := "-"
			if service.Port > 0 {
				portStr = fmt.Sprintf("%d", service.Port)
			}
			fmt.Printf("│ %-15s │ %-8s │ %-9d │ %s%-6s%s │ %-32s │\n",
				service.Name, portStr, service.Instances, statusColor, service.Status, Reset, healthBar)
		}
	}
	fmt.Printf("└─────────────────┴──────────┴───────────┴────────┴──────────────────────────────────┘\n\n")

	// Kafka Metrics
	fmt.Printf("%s%s▌ KAFKA/REDPANDA METRICS%s\n", Bold, Blue, Reset)
	fmt.Printf("┌─────────────────┬──────────────┬──────────────┬──────────────┬──────────────────┐\n")
	fmt.Printf("│ Topics          │ Partitions   │ Messages/sec │ Consumer Lag │ Brokers          │\n")
	fmt.Printf("├─────────────────┼──────────────┼──────────────┼──────────────┼──────────────────┤\n")
	
	lagColor := Green
	if metrics.Kafka.Lag > 100 {
		lagColor = Yellow
	}
	if metrics.Kafka.Lag > 1000 {
		lagColor = Red
	}
	
	fmt.Printf("│ %-15d │ %-12d │ %-12d │ %s%-12d%s │ %-16d │\n",
		metrics.Kafka.Topics, metrics.Kafka.Partitions, 
		metrics.EventsIngested.Load()/10, // Approximate rate
		lagColor, metrics.Kafka.Lag, Reset, 1)
	fmt.Printf("└─────────────────┴──────────────┴──────────────┴──────────────┴──────────────────┘\n\n")

	// Performance Metrics
	fmt.Printf("%s%s▌ PERFORMANCE METRICS%s\n", Bold, Blue, Reset)
	fmt.Printf("┌──────────────────────┬──────────────────────┬──────────────────────┬────────────────┐\n")
	fmt.Printf("│ Events Ingested      │ Events Processed     │ Queries Executed     │ Error Rate     │\n")
	fmt.Printf("├──────────────────────┼──────────────────────┼──────────────────────┼────────────────┤\n")
	fmt.Printf("│ %-20d │ %-20d │ %-20d │ %-14.2f%%│\n",
		metrics.EventsIngested.Load(), metrics.EventsProcessed.Load(),
		metrics.QueryCount.Load(), float64(metrics.ErrorRate.Load())/100)
	fmt.Printf("└──────────────────────┴──────────────────────┴──────────────────────┴────────────────┘\n\n")

	// Resource Usage
	fmt.Printf("%s%s▌ RESOURCE USAGE%s\n", Bold, Blue, Reset)
	fmt.Printf("┌──────────────────────────────────────────────────────────────────────────────────────┐\n")
	fmt.Printf("│ CPU Usage:    %s%-60s%s %5.1f%% │\n", 
		getUsageColor(metrics.CPUUsage), 
		generateProgressBar(metrics.CPUUsage, 100, 60), 
		Reset, metrics.CPUUsage)
	fmt.Printf("│ Memory Usage: %s%-60s%s %5.1f%% │\n", 
		getUsageColor(metrics.MemoryUsage), 
		generateProgressBar(metrics.MemoryUsage, 100, 60), 
		Reset, metrics.MemoryUsage)
	fmt.Printf("└──────────────────────────────────────────────────────────────────────────────────────┘\n\n")

	// Load Test Status (if active)
	if metrics.LoadTestActive {
		fmt.Printf("%s%s▌ LOAD TEST STATUS%s\n", Bold, Magenta, Reset)
		fmt.Printf("┌──────────────────┬──────────────────┬──────────────────┬──────────────────────────┐\n")
		fmt.Printf("│ Target Rate      │ Events Sent      │ Failed           │ Success Rate             │\n")
		fmt.Printf("├──────────────────┼──────────────────┼──────────────────┼──────────────────────────┤\n")
		
		sent := metrics.LoadTestSent.Load()
		failed := metrics.LoadTestFailed.Load()
		successRate := 100.0
		if sent > 0 {
			successRate = float64(sent-failed) * 100 / float64(sent)
		}
		
		successColor := Green
		if successRate < 99 {
			successColor = Yellow
		}
		if successRate < 95 {
			successColor = Red
		}
		
		fmt.Printf("│ %-16d │ %-16d │ %-16d │ %s%-24.2f%%%s │\n",
			metrics.LoadTestRate, sent, failed, successColor, successRate, Reset)
		fmt.Printf("└──────────────────┴──────────────────┴──────────────────┴──────────────────────────┘\n\n")
	}

	// Data Flow Visualization
	fmt.Printf("%s%s▌ DATA FLOW%s\n", Bold, Blue, Reset)
	fmt.Printf("┌──────────────────────────────────────────────────────────────────────────────────────┐\n")
	fmt.Printf("│                                                                                      │\n")
	fmt.Printf("│  Client ──%s→%s Gateway ──%s→%s Kafka ──%s→%s Ingester ──%s→%s Hot-Tier ──%s→%s Query API ──%s→%s Client  │\n",
		getFlowColor(metrics.Services["Gateway"].Health), Reset,
		getFlowColor(metrics.Services["Redpanda"].Health), Reset,
		getFlowColor(metrics.Services["Stream-Ingester"].Health), Reset,
		getFlowColor(metrics.Services["Hot-Tier"].Health), Reset,
		getFlowColor(metrics.Services["Query-API"].Health), Reset,
		Green, Reset)
	fmt.Printf("│           %s[%.0f/s]%s      %s[%.0f/s]%s     %s[%.0f/s]%s       %s[%.0f/s]%s      %s[%.0f/s]%s                    │\n",
		Green, float64(metrics.EventsIngested.Load())/10, Reset,
		Green, float64(metrics.EventsIngested.Load())/10, Reset,
		Green, float64(metrics.EventsProcessed.Load())/10, Reset,
		Green, float64(metrics.EventsProcessed.Load())/10, Reset,
		Green, float64(metrics.QueryCount.Load())/10, Reset)
	fmt.Printf("│                                                                                      │\n")
	fmt.Printf("└──────────────────────────────────────────────────────────────────────────────────────┘\n\n")

	// Footer
	fmt.Printf("%s%sPress Ctrl+C to exit | Updated every second | Pipeline: OPERATIONAL%s\n", Bold, Yellow, Reset)
}

func generateProgressBar(value, max float64, width int) string {
	if value > max {
		value = max
	}
	filled := int(value * float64(width) / max)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return bar
}

func generateHealthBar(healthy bool) string {
	if healthy {
		return Green + "████████████████████████████████" + Reset
	}
	return Red + "████████████████████████████████" + Reset
}

func getUsageColor(usage float64) string {
	if usage > 80 {
		return Red
	}
	if usage > 60 {
		return Yellow
	}
	return Green
}

func getFlowColor(healthy bool) string {
	if healthy {
		return Green + "━━"
	}
	return Red + "╳╳"
}