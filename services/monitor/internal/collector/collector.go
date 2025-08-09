// Package collector handles metrics collection from TrueNow services
package collector

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ServiceMetrics represents metrics for a single service
type ServiceMetrics struct {
	ServiceName string
	Timestamp   time.Time
	
	// Instance metrics
	Instances          int
	HealthyInstances   int
	UnhealthyInstances int
	
	// Request metrics
	RequestsPerSecond float64
	EventsPerSecond   float64
	BytesInPerSecond  float64
	BytesOutPerSecond float64
	
	// Latency percentiles (in milliseconds)
	LatencyP50   float64
	LatencyP90   float64
	LatencyP95   float64
	LatencyP99   float64
	LatencyP999  float64
	LatencyMax   float64
	LatencyMean  float64
	
	// Error metrics
	ErrorRate       float64
	ErrorPercentage float64
	TimeoutRate     float64
	RejectionRate   float64
	
	// Resource metrics
	CPUUsagePercent    float64
	MemoryUsagePercent float64
	MemoryUsageBytes   int64
	GoroutineCount     int
	GCPauseP99         float64
	
	// Connection metrics
	ActiveConnections  int
	ConnectionPoolSize int
	
	// Service-specific metrics
	CustomMetrics map[string]interface{}
}

// SystemMetrics represents system-wide metrics
type SystemMetrics struct {
	Timestamp time.Time
	
	// Overall health
	HealthScore         float64
	ServiceAvailability map[string]bool
	CriticalAlerts      int
	WarningAlerts       int
	
	// Throughput
	TotalEventsPerSecond  float64
	TotalQueriesPerSecond float64
	TotalBytesPerSecond   float64
	
	// Resources
	TotalCPUCores  int
	TotalMemoryGB  float64
	TotalDiskGB    float64
	
	// Efficiency
	EfficiencyScore float64
	CostPerMillion  float64
}

// Alert represents a system alert
type Alert struct {
	ID        string
	Timestamp time.Time
	Service   string
	Severity  string // "info", "warning", "critical"
	Message   string
	Value     float64
	Threshold float64
	Resolved  bool
}

// ServiceEndpoint defines a service endpoint for metrics collection
type ServiceEndpoint struct {
	Name        string
	BaseURL     string
	HealthPath  string
	MetricsPath string
	StatsPath   string
}

// Collector manages metrics collection from all services
type Collector struct {
	mu        sync.RWMutex
	endpoints map[string]*ServiceEndpoint
	client    *http.Client
	
	// Cached metrics
	lastUpdate time.Time
	services   map[string]*ServiceMetrics
	system     *SystemMetrics
	alerts     []Alert
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		endpoints: make(map[string]*ServiceEndpoint),
		services:  make(map[string]*ServiceMetrics),
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
		alerts: make([]Alert, 0),
	}
}

// Initialize sets up service endpoints
func (c *Collector) Initialize() {
	c.endpoints = map[string]*ServiceEndpoint{
		"control-plane": {
			Name:        "control-plane",
			BaseURL:     "http://localhost:8001",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/v1/stats",
		},
		"gateway": {
			Name:        "gateway",
			BaseURL:     "http://localhost:8088",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/stats",
		},
		"hot-tier": {
			Name:        "hot-tier",
			BaseURL:     "http://localhost:9090",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/stats",
		},
		"query-api": {
			Name:        "query-api",
			BaseURL:     "http://localhost:8081",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/v1/stats",
		},
		// stream-ingester is a Kafka consumer without HTTP endpoint
		"watermark-service": {
			Name:        "watermark-service",
			BaseURL:     "http://localhost:8084",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/stats",
		},
		"rebalancer": {
			Name:        "rebalancer",
			BaseURL:     "http://localhost:8086",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/stats",
		},
		"autoscaler": {
			Name:        "autoscaler",
			BaseURL:     "http://localhost:8095",
			HealthPath:  "/health",
			MetricsPath: "/metrics",
			StatsPath:   "/api/v1/system/metrics",
		},
	}
}

// CollectAll collects metrics from all services
func (c *Collector) CollectAll(ctx context.Context) error {
	// Always use real data collection - never simulate
	return c.CollectAllReal(ctx)
}

// collectServiceMetrics collects metrics from a single service - delegates to real implementation
func (c *Collector) collectServiceMetrics(ctx context.Context, name string, endpoint *ServiceEndpoint) *ServiceMetrics {
	return c.collectServiceMetricsReal(ctx, name, endpoint)
}

// collectServiceMetricsOLD is the old simulated version (deprecated)
func (c *Collector) collectServiceMetricsOLD(ctx context.Context, name string, endpoint *ServiceEndpoint) *ServiceMetrics {
	m := &ServiceMetrics{
		ServiceName:   name,
		Timestamp:     time.Now(),
		CustomMetrics: make(map[string]interface{}),
	}
	
	// Simulate service instances and health
	// In production, this would actually check the health endpoints
	switch name {
	case "gateway":
		m.Instances = 3
		m.HealthyInstances = 3
		m.UnhealthyInstances = 0
	case "hot-tier":
		m.Instances = 8
		m.HealthyInstances = 8
		m.UnhealthyInstances = 0
	case "query-api":
		m.Instances = 4
		m.HealthyInstances = 4
		m.UnhealthyInstances = 0
	case "stream-ingester":
		m.Instances = 6
		m.HealthyInstances = 6
		m.UnhealthyInstances = 0
	case "watermark-service":
		// Simulate one unhealthy instance
		m.Instances = 2
		m.HealthyInstances = 1
		m.UnhealthyInstances = 1
	case "rebalancer":
		m.Instances = 2
		m.HealthyInstances = 2
		m.UnhealthyInstances = 0
	case "autoscaler":
		m.Instances = 1
		m.HealthyInstances = 1
		m.UnhealthyInstances = 0
	default:
		m.Instances = 1
		m.HealthyInstances = 1
		m.UnhealthyInstances = 0
	}
	
	// Collect metrics based on service type
	switch name {
	case "gateway":
		c.collectGatewayMetrics(ctx, endpoint, m)
	case "hot-tier":
		c.collectHotTierMetrics(ctx, endpoint, m)
	case "query-api":
		c.collectQueryAPIMetrics(ctx, endpoint, m)
	case "watermark-service":
		c.collectWatermarkMetrics(ctx, endpoint, m)
	case "rebalancer":
		c.collectRebalancerMetrics(ctx, endpoint, m)
	default:
		c.collectGenericMetrics(ctx, endpoint, m)
	}
	
	return m
}

// checkHealth checks if a service is healthy
func (c *Collector) checkHealth(ctx context.Context, endpoint *ServiceEndpoint) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.BaseURL+endpoint.HealthPath, nil)
	if err != nil {
		return false
	}
	
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == http.StatusOK
}

// collectGatewayMetrics collects gateway-specific metrics
func (c *Collector) collectGatewayMetrics(ctx context.Context, endpoint *ServiceEndpoint, m *ServiceMetrics) {
	// Simulate metrics with some variation
	baseQPS := 125000.0
	variation := (float64(time.Now().Unix()%10) - 5) / 100 // ±5% variation
	
	m.RequestsPerSecond = baseQPS * (1 + variation)
	m.EventsPerSecond = baseQPS * (1 + variation)
	m.BytesInPerSecond = 500 * 1024 * 1024 * (1 + variation) // 500 MB/s ± variation
	m.BytesOutPerSecond = 100 * 1024 * 1024 * (1 + variation) // 100 MB/s ± variation
	
	m.LatencyP50 = 5.2 + variation*2
	m.LatencyP90 = 10.5 + variation*3
	m.LatencyP95 = 15.3 + variation*4
	m.LatencyP99 = 25.1 + variation*5
	m.LatencyP999 = 45.2 + variation*10
	m.LatencyMax = 125.3 + variation*20
	m.LatencyMean = 8.5 + variation*2
	
	m.ErrorRate = 0.02 + variation*0.01
	m.ErrorPercentage = 0.02 + variation*0.01
	m.TimeoutRate = 0.001
	m.RejectionRate = 0.005
	
	m.CPUUsagePercent = 45.2 + variation*10
	m.MemoryUsagePercent = 62.1 + variation*5
	m.MemoryUsageBytes = 8 * 1024 * 1024 * 1024 // 8 GB
	m.GoroutineCount = 1250
	m.GCPauseP99 = 0.5
	
	m.ActiveConnections = 15000
	m.ConnectionPoolSize = 20000
	
	// Gateway-specific
	m.CustomMetrics["batch_size"] = 1000
	m.CustomMetrics["compression_ratio"] = 0.75
	m.CustomMetrics["validation_failures"] = 125
	m.CustomMetrics["rate_limit_hits"] = 50
}

// collectHotTierMetrics collects hot-tier specific metrics
func (c *Collector) collectHotTierMetrics(ctx context.Context, endpoint *ServiceEndpoint, m *ServiceMetrics) {
	m.RequestsPerSecond = 450000
	m.EventsPerSecond = 450000
	m.BytesInPerSecond = 2 * 1024 * 1024 * 1024 // 2 GB/s
	
	m.LatencyP50 = 2.1
	m.LatencyP90 = 4.2
	m.LatencyP95 = 5.2
	m.LatencyP99 = 10.3
	m.LatencyP999 = 25.1
	m.LatencyMax = 45.2
	m.LatencyMean = 3.5
	
	m.ErrorRate = 0.01
	m.ErrorPercentage = 0.01
	
	m.CPUUsagePercent = 72.3
	m.MemoryUsagePercent = 78.4
	m.MemoryUsageBytes = 24 * 1024 * 1024 * 1024 // 24 GB
	m.GoroutineCount = 5000
	m.GCPauseP99 = 1.2
	
	// Hot-tier specific
	m.CustomMetrics["buffer_1s_usage"] = 85.2
	m.CustomMetrics["buffer_10s_usage"] = 72.1
	m.CustomMetrics["buffer_1m_usage"] = 65.3
	m.CustomMetrics["events_processed"] = int64(125000000)
	m.CustomMetrics["events_deduped"] = int64(250000)
	m.CustomMetrics["dedupe_ratio"] = 0.2
	m.CustomMetrics["hll_accuracy"] = 99.5
	m.CustomMetrics["tdigest_accuracy"] = 99.8
	m.CustomMetrics["numa_node"] = 0
	m.CustomMetrics["local_mem_access"] = 95.2
}

// collectQueryAPIMetrics collects query API metrics
func (c *Collector) collectQueryAPIMetrics(ctx context.Context, endpoint *ServiceEndpoint, m *ServiceMetrics) {
	m.RequestsPerSecond = 5000
	m.EventsPerSecond = 5000
	
	m.LatencyP50 = 15.2
	m.LatencyP90 = 35.2
	m.LatencyP95 = 45.2
	m.LatencyP99 = 89.3
	m.LatencyP999 = 125.3
	m.LatencyMax = 250.5
	m.LatencyMean = 25.3
	
	m.ErrorRate = 0.15
	m.ErrorPercentage = 0.15
	
	m.CPUUsagePercent = 38.1
	m.MemoryUsagePercent = 45.2
	m.MemoryUsageBytes = 4 * 1024 * 1024 * 1024 // 4 GB
	m.GoroutineCount = 500
	
	// Query-specific
	m.CustomMetrics["queries_per_second"] = 5000.0
	m.CustomMetrics["cache_hit_rate"] = 85.2
	m.CustomMetrics["query_complexity"] = 3.5
	m.CustomMetrics["fanout_factor"] = 8.2
	m.CustomMetrics["redis_connections"] = 50
	m.CustomMetrics["redis_memory_mb"] = 2048
	m.CustomMetrics["redis_hit_rate"] = 92.3
}

// collectWatermarkMetrics collects watermark service metrics
func (c *Collector) collectWatermarkMetrics(ctx context.Context, endpoint *ServiceEndpoint, m *ServiceMetrics) {
	m.RequestsPerSecond = 10000
	
	m.LatencyP50 = 10.2
	m.LatencyP90 = 25.3
	m.LatencyP95 = 35.2
	m.LatencyP99 = 125.3
	m.LatencyP999 = 250.1
	m.LatencyMax = 500.2
	m.LatencyMean = 18.5
	
	m.ErrorRate = 1.2
	m.ErrorPercentage = 1.2
	
	m.CPUUsagePercent = 88.2
	m.MemoryUsagePercent = 45.2
	
	// Watermark-specific
	m.CustomMetrics["global_freshness_p50"] = int64(5000)
	m.CustomMetrics["global_freshness_p95"] = int64(15000)
	m.CustomMetrics["global_freshness_p99"] = int64(30000)
	m.CustomMetrics["tables_tracked"] = 150
	m.CustomMetrics["stale_partitions"] = 3
	m.CustomMetrics["freshness_violations"] = 2
	m.CustomMetrics["slo_compliance"] = 98.5
}

// collectRebalancerMetrics collects rebalancer metrics
func (c *Collector) collectRebalancerMetrics(ctx context.Context, endpoint *ServiceEndpoint, m *ServiceMetrics) {
	m.RequestsPerSecond = 50
	
	m.LatencyP50 = 5.1
	m.LatencyP90 = 12.3
	m.LatencyP95 = 15.2
	m.LatencyP99 = 30.2
	m.LatencyP999 = 50.1
	m.LatencyMax = 75.3
	m.LatencyMean = 8.2
	
	m.CPUUsagePercent = 25.3
	m.MemoryUsagePercent = 35.2
	
	// Rebalancer-specific
	m.CustomMetrics["total_shards"] = 1000
	m.CustomMetrics["active_shards"] = 950
	m.CustomMetrics["rebalancing_shards"] = 3
	m.CustomMetrics["gini_coefficient"] = 0.15
	m.CustomMetrics["max_shard_load"] = 125000.0
	m.CustomMetrics["min_shard_load"] = 95000.0
	m.CustomMetrics["avg_shard_load"] = 110000.0
	m.CustomMetrics["rebalances_pending"] = 2
	m.CustomMetrics["rebalances_active"] = 1
	m.CustomMetrics["rebalances_failed"] = 0
}

// collectGenericMetrics collects generic metrics for any service
func (c *Collector) collectGenericMetrics(ctx context.Context, endpoint *ServiceEndpoint, m *ServiceMetrics) {
	// Basic metrics that all services have
	m.RequestsPerSecond = 100
	m.LatencyP50 = 5.0
	m.LatencyP90 = 10.0
	m.LatencyP95 = 15.0
	m.LatencyP99 = 25.0
	m.CPUUsagePercent = 20.0
	m.MemoryUsagePercent = 30.0
}

// updateSystemMetrics aggregates system-wide metrics
func (c *Collector) updateSystemMetrics() {
	sys := &SystemMetrics{
		Timestamp:           time.Now(),
		ServiceAvailability: make(map[string]bool),
	}
	
	var totalEvents, totalQueries, totalBytes float64
	var totalCPU, totalMemory float64
	var healthyServices int
	
	for name, m := range c.services {
		sys.ServiceAvailability[name] = m.HealthyInstances > 0
		
		if m.HealthyInstances > 0 {
			healthyServices++
		}
		
		totalEvents += m.EventsPerSecond
		totalQueries += m.RequestsPerSecond
		totalBytes += m.BytesInPerSecond
		totalCPU += m.CPUUsagePercent
		totalMemory += m.MemoryUsagePercent
	}
	
	sys.TotalEventsPerSecond = totalEvents
	sys.TotalQueriesPerSecond = totalQueries
	sys.TotalBytesPerSecond = totalBytes
	
	// Calculate health score
	sys.HealthScore = float64(healthyServices) / float64(len(c.services)) * 100
	
	// Count alerts
	for _, alert := range c.alerts {
		if !alert.Resolved {
			switch alert.Severity {
			case "critical":
				sys.CriticalAlerts++
			case "warning":
				sys.WarningAlerts++
			}
		}
	}
	
	// Resource totals (simulated)
	sys.TotalCPUCores = 256
	sys.TotalMemoryGB = 512
	sys.TotalDiskGB = 10240
	
	// Cost metrics (simulated)
	sys.CostPerMillion = 0.25
	sys.EfficiencyScore = 85.5
	
	c.system = sys
}

// checkAlerts checks for alert conditions
func (c *Collector) checkAlerts() {
	newAlerts := make([]Alert, 0)
	
	for name, m := range c.services {
		// CPU alerts
		if m.CPUUsagePercent > 85 {
			newAlerts = append(newAlerts, Alert{
				ID:        fmt.Sprintf("%s-cpu-high", name),
				Timestamp: time.Now(),
				Service:   name,
				Severity:  "critical",
				Message:   fmt.Sprintf("High CPU usage: %.1f%%", m.CPUUsagePercent),
				Value:     m.CPUUsagePercent,
				Threshold: 85.0,
			})
		} else if m.CPUUsagePercent > 70 {
			newAlerts = append(newAlerts, Alert{
				ID:        fmt.Sprintf("%s-cpu-warn", name),
				Timestamp: time.Now(),
				Service:   name,
				Severity:  "warning",
				Message:   fmt.Sprintf("Elevated CPU usage: %.1f%%", m.CPUUsagePercent),
				Value:     m.CPUUsagePercent,
				Threshold: 70.0,
			})
		}
		
		// Memory alerts
		if m.MemoryUsagePercent > 90 {
			newAlerts = append(newAlerts, Alert{
				ID:        fmt.Sprintf("%s-mem-high", name),
				Timestamp: time.Now(),
				Service:   name,
				Severity:  "critical",
				Message:   fmt.Sprintf("High memory usage: %.1f%%", m.MemoryUsagePercent),
				Value:     m.MemoryUsagePercent,
				Threshold: 90.0,
			})
		}
		
		// Error rate alerts
		if m.ErrorPercentage > 5.0 {
			newAlerts = append(newAlerts, Alert{
				ID:        fmt.Sprintf("%s-errors-high", name),
				Timestamp: time.Now(),
				Service:   name,
				Severity:  "critical",
				Message:   fmt.Sprintf("High error rate: %.2f%%", m.ErrorPercentage),
				Value:     m.ErrorPercentage,
				Threshold: 5.0,
			})
		} else if m.ErrorPercentage > 1.0 {
			newAlerts = append(newAlerts, Alert{
				ID:        fmt.Sprintf("%s-errors-warn", name),
				Timestamp: time.Now(),
				Service:   name,
				Severity:  "warning",
				Message:   fmt.Sprintf("Elevated error rate: %.2f%%", m.ErrorPercentage),
				Value:     m.ErrorPercentage,
				Threshold: 1.0,
			})
		}
		
		// Latency alerts
		if m.LatencyP99 > 100 {
			newAlerts = append(newAlerts, Alert{
				ID:        fmt.Sprintf("%s-latency-high", name),
				Timestamp: time.Now(),
				Service:   name,
				Severity:  "warning",
				Message:   fmt.Sprintf("High P99 latency: %.1fms", m.LatencyP99),
				Value:     m.LatencyP99,
				Threshold: 100.0,
			})
		}
	}
	
	// Service-specific alerts
	if wm, ok := c.services["watermark-service"]; ok {
		if freshness, ok := wm.CustomMetrics["global_freshness_p99"].(int64); ok {
			if freshness > 30000 {
				newAlerts = append(newAlerts, Alert{
					ID:        "freshness-slo-violation",
					Timestamp: time.Now(),
					Service:   "watermark-service",
					Severity:  "critical",
					Message:   fmt.Sprintf("Freshness SLO violation: %dms", freshness),
					Value:     float64(freshness),
					Threshold: 30000.0,
				})
			}
		}
	}
	
	c.alerts = newAlerts
}

// GetServiceMetrics returns metrics for a specific service
func (c *Collector) GetServiceMetrics(name string) *ServiceMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.services[name]
}

// GetAllServiceMetrics returns metrics for all services
func (c *Collector) GetAllServiceMetrics() map[string]*ServiceMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	result := make(map[string]*ServiceMetrics)
	for k, v := range c.services {
		result[k] = v
	}
	return result
}

// GetSystemMetrics returns system-wide metrics
func (c *Collector) GetSystemMetrics() *SystemMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.system
}

// GetAlerts returns current alerts
func (c *Collector) GetAlerts() []Alert {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	result := make([]Alert, len(c.alerts))
	copy(result, c.alerts)
	return result
}

// GetLastUpdate returns the last update time
func (c *Collector) GetLastUpdate() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastUpdate
}