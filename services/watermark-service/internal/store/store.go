package store

import (
	"sort"
	"sync"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/pkg/types"
)

// WatermarkStore manages watermark data in memory
type WatermarkStore struct {
	mu sync.RWMutex
	
	// Service watermarks indexed by service ID
	serviceWatermarks map[string]*types.ServiceWatermark
	
	// Table watermarks indexed by namespace.table
	tableWatermarks map[string]*types.TableWatermark
	
	// Freshness configs indexed by namespace.table
	freshnessConfigs map[string]*types.FreshnessConfig
	
	// SLO violations
	violations []types.SLOViolation
	
	// Metrics
	updateCount int64
	lastUpdate  time.Time
}

// NewWatermarkStore creates a new watermark store
func NewWatermarkStore() *WatermarkStore {
	return &WatermarkStore{
		serviceWatermarks: make(map[string]*types.ServiceWatermark),
		tableWatermarks:   make(map[string]*types.TableWatermark),
		freshnessConfigs:  make(map[string]*types.FreshnessConfig),
		violations:        make([]types.SLOViolation, 0),
	}
}

// UpdateWatermark is a simplified interface for updating watermarks
func (s *WatermarkStore) UpdateWatermark(service, namespace, table string, watermarkMs int64, labels map[string]string) {
	update := &types.WatermarkUpdate{
		ServiceID:      service,
		ServiceType:    "ingester",
		Namespace:      namespace,
		Table:          table,
		EventTime:      watermarkMs,
		ProcessingTime: time.Now().UnixMilli(),
		EventCount:     1,
		Timestamp:      time.Now(),
	}
	s.UpdateServiceWatermark(update)
}

// GetAllWatermarks returns all current watermarks
func (s *WatermarkStore) GetAllWatermarks() map[string]*types.ServiceWatermark {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	result := make(map[string]*types.ServiceWatermark)
	for k, v := range s.serviceWatermarks {
		result[k] = v
	}
	return result
}

// GetTableWatermarks returns watermarks for a specific table
func (s *WatermarkStore) GetTableWatermarks(namespace, table string) *types.TableWatermark {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	tableKey := namespace + "." + table
	if tw, exists := s.tableWatermarks[tableKey]; exists {
		return tw
	}
	return nil
}

// GetGlobalStats returns global statistics
func (s *WatermarkStore) GetGlobalStats() *types.GlobalStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	stats := &types.GlobalStats{
		TotalServices:    len(s.serviceWatermarks),
		TotalTables:      len(s.tableWatermarks),
		TotalViolations:  len(s.violations),
		LastUpdate:       s.lastUpdate,
		UpdateCount:      s.updateCount,
	}
	
	// Calculate total events and pending
	for _, sw := range s.serviceWatermarks {
		stats.TotalEvents += sw.EventCount
		stats.TotalPending += sw.PendingCount
	}
	
	return stats
}

// GetSLOViolations returns current SLO violations
func (s *WatermarkStore) GetSLOViolations() []types.SLOViolation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	violations := make([]types.SLOViolation, len(s.violations))
	copy(violations, s.violations)
	return violations
}

// UpdateServiceWatermark updates watermark for a service/shard
func (s *WatermarkStore) UpdateServiceWatermark(update *types.WatermarkUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	serviceID := update.ServiceID
	tableKey := update.Namespace + "." + update.Table
	
	// Update or create service watermark
	sw, exists := s.serviceWatermarks[serviceID]
	if !exists {
		sw = &types.ServiceWatermark{
			ServiceID:   serviceID,
			ServiceType: update.ServiceType,
			Namespace:   update.Namespace,
			Table:       update.Table,
			ShardID:     update.ShardID,
		}
		s.serviceWatermarks[serviceID] = sw
	}
	
	// Update watermarks
	if update.EventTime > sw.HighWatermark {
		sw.HighWatermark = update.EventTime
	}
	if sw.LowWatermark == 0 || update.EventTime < sw.LowWatermark {
		sw.LowWatermark = update.EventTime
	}
	
	sw.EventCount += update.EventCount
	sw.PendingCount = update.PendingCount
	sw.ProcessingTime = update.ProcessingTime
	sw.LastUpdate = update.Timestamp
	sw.IsHealthy = s.isServiceHealthy(sw)
	
	// Update table watermark
	s.updateTableWatermark(tableKey)
	
	s.updateCount++
	s.lastUpdate = time.Now()
}

// updateTableWatermark recalculates table-level watermark
func (s *WatermarkStore) updateTableWatermark(tableKey string) {
	tw, exists := s.tableWatermarks[tableKey]
	if !exists {
		parts := splitTableKey(tableKey)
		if len(parts) != 2 {
			return
		}
		tw = &types.TableWatermark{
			Namespace: parts[0],
			Table:     parts[1],
		}
		s.tableWatermarks[tableKey] = tw
	}
	
	// Collect all shards for this table
	var shardWatermarks []types.ServiceWatermark
	var watermarks []int64
	var freshnesses []int64
	totalEvents := int64(0)
	totalPending := int64(0)
	healthyShards := 0
	
	for _, sw := range s.serviceWatermarks {
		if sw.Namespace == tw.Namespace && sw.Table == tw.Table {
			shardWatermarks = append(shardWatermarks, *sw)
			watermarks = append(watermarks, sw.HighWatermark)
			
			// Calculate freshness for this shard
			freshness := sw.ProcessingTime - sw.HighWatermark
			if freshness > 0 {
				freshnesses = append(freshnesses, freshness)
			}
			
			totalEvents += sw.EventCount
			totalPending += sw.PendingCount
			
			if sw.IsHealthy {
				healthyShards++
			}
		}
	}
	
	if len(watermarks) == 0 {
		return
	}
	
	// Calculate min/max/median watermarks
	sort.Slice(watermarks, func(i, j int) bool { return watermarks[i] < watermarks[j] })
	tw.MinWatermark = watermarks[0]
	tw.MaxWatermark = watermarks[len(watermarks)-1]
	tw.MedianWatermark = watermarks[len(watermarks)/2]
	
	// Calculate freshness percentiles
	if len(freshnesses) > 0 {
		sort.Slice(freshnesses, func(i, j int) bool { return freshnesses[i] < freshnesses[j] })
		tw.FreshnessP50 = percentile(freshnesses, 50)
		tw.FreshnessP95 = percentile(freshnesses, 95)
		tw.FreshnessP99 = percentile(freshnesses, 99)
	}
	
	// Update metrics
	tw.ProcessingTime = time.Now().UnixMilli()
	tw.TotalEventCount = totalEvents
	tw.TotalPendingCount = totalPending
	tw.ActiveShards = len(shardWatermarks)
	tw.HealthyShards = healthyShards
	tw.LastUpdate = time.Now()
	tw.ShardWatermarks = shardWatermarks
	
	// Calculate events per second (simple moving average)
	if tw.EventsPerSecond == 0 {
		tw.EventsPerSecond = float64(totalEvents) / 60.0 // Initial estimate
	} else {
		// Exponential moving average
		alpha := 0.1
		newRate := float64(totalEvents-tw.TotalEventCount) / time.Since(tw.LastUpdate).Seconds()
		tw.EventsPerSecond = alpha*newRate + (1-alpha)*tw.EventsPerSecond
	}
	
	// Check for SLO violations
	s.checkSLOViolation(tw)
}

// checkSLOViolation checks if table violates freshness SLO
func (s *WatermarkStore) checkSLOViolation(tw *types.TableWatermark) {
	tableKey := tw.Namespace + "." + tw.Table
	config, exists := s.freshnessConfigs[tableKey]
	if !exists {
		// Use default SLOs if not configured
		config = &types.FreshnessConfig{
			Namespace:          tw.Namespace,
			Table:              tw.Table,
			TargetFreshnessP99: 30000, // 30 seconds default
			AlertThreshold:     60000, // 1 minute warning
			CriticalThreshold:  120000, // 2 minutes critical
		}
	}
	
	// Check P99 freshness
	if tw.FreshnessP99 > config.TargetFreshnessP99 {
		severity := "warning"
		if tw.FreshnessP99 > config.CriticalThreshold {
			severity = "critical"
		}
		
		// Check if we already have an active violation
		violationExists := false
		for i, v := range s.violations {
			if v.Namespace == tw.Namespace && v.Table == tw.Table {
				// Update existing violation
				s.violations[i].ActualFreshness = tw.FreshnessP99
				s.violations[i].Duration = time.Since(v.ViolationStart).Milliseconds()
				s.violations[i].Severity = severity
				violationExists = true
				break
			}
		}
		
		if !violationExists {
			// Create new violation
			s.violations = append(s.violations, types.SLOViolation{
				Namespace:       tw.Namespace,
				Table:           tw.Table,
				SLOTarget:       config.TargetFreshnessP99,
				ActualFreshness: tw.FreshnessP99,
				ViolationStart:  time.Now(),
				Duration:        0,
				Severity:        severity,
			})
		}
	} else {
		// Clear violation if freshness is back to normal
		newViolations := []types.SLOViolation{}
		for _, v := range s.violations {
			if !(v.Namespace == tw.Namespace && v.Table == tw.Table) {
				newViolations = append(newViolations, v)
			}
		}
		s.violations = newViolations
	}
}

// GetTableWatermark returns watermark for a specific table
func (s *WatermarkStore) GetTableWatermark(namespace, table string) (*types.TableWatermark, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	tableKey := namespace + "." + table
	tw, exists := s.tableWatermarks[tableKey]
	if !exists {
		return nil, false
	}
	
	// Return a copy
	result := *tw
	return &result, true
}

// GetSystemWatermark returns system-wide watermark state
func (s *WatermarkStore) GetSystemWatermark() *types.SystemWatermark {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	sw := &types.SystemWatermark{
		ProcessingTime:  time.Now().UnixMilli(),
		TableWatermarks: make(map[string]*types.TableWatermark),
		LastUpdate:      s.lastUpdate,
	}
	
	var allFreshnesses []int64
	minWatermark := int64(^uint64(0) >> 1) // MaxInt64
	maxWatermark := int64(0)
	totalEventsPerSec := 0.0
	healthyTables := 0
	
	// Aggregate across all tables
	for key, tw := range s.tableWatermarks {
		sw.TableWatermarks[key] = tw
		
		if tw.MinWatermark < minWatermark {
			minWatermark = tw.MinWatermark
		}
		if tw.MaxWatermark > maxWatermark {
			maxWatermark = tw.MaxWatermark
		}
		
		totalEventsPerSec += tw.EventsPerSecond
		
		if tw.FreshnessP99 > 0 {
			allFreshnesses = append(allFreshnesses, tw.FreshnessP99)
		}
		
		if tw.HealthyShards == tw.ActiveShards && tw.ActiveShards > 0 {
			healthyTables++
		}
	}
	
	sw.MinWatermark = minWatermark
	sw.MaxWatermark = maxWatermark
	sw.TotalEventsPerSec = totalEventsPerSec
	sw.TotalTables = len(s.tableWatermarks)
	sw.HealthyTables = healthyTables
	
	// Calculate global freshness percentiles
	if len(allFreshnesses) > 0 {
		sort.Slice(allFreshnesses, func(i, j int) bool { return allFreshnesses[i] < allFreshnesses[j] })
		sw.GlobalFreshnessP50 = percentile(allFreshnesses, 50)
		sw.GlobalFreshnessP95 = percentile(allFreshnesses, 95)
		sw.GlobalFreshnessP99 = percentile(allFreshnesses, 99)
	}
	
	// Copy violations
	sw.ViolatedSLOs = make([]types.SLOViolation, len(s.violations))
	copy(sw.ViolatedSLOs, s.violations)
	
	return sw
}

// GetHealthStatus returns health status of watermark tracking
func (s *WatermarkStore) GetHealthStatus() *types.HealthStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	status := &types.HealthStatus{
		IsHealthy:      true,
		LastUpdateTime: s.lastUpdate,
		StaleServices:  []string{},
		UnhealthyTables: []string{},
	}
	
	now := time.Now()
	staleThreshold := 30 * time.Second
	
	// Check for stale services
	for id, sw := range s.serviceWatermarks {
		if now.Sub(sw.LastUpdate) > staleThreshold {
			status.StaleServices = append(status.StaleServices, id)
			status.IsHealthy = false
		}
	}
	
	// Check for unhealthy tables
	for key, tw := range s.tableWatermarks {
		if tw.HealthyShards < tw.ActiveShards/2 {
			status.UnhealthyTables = append(status.UnhealthyTables, key)
			status.IsHealthy = false
		}
	}
	
	// Get system freshness
	sw := s.GetSystemWatermark()
	status.SystemFreshnessP99 = sw.GlobalFreshnessP99
	
	// Check if system freshness violates global SLO
	if sw.GlobalFreshnessP99 > 30000 { // 30 seconds
		status.IsHealthy = false
		status.Message = "System freshness P99 exceeds 30 seconds"
	} else if len(status.StaleServices) > 0 {
		status.Message = "Some services have stale watermarks"
	} else if len(status.UnhealthyTables) > 0 {
		status.Message = "Some tables have unhealthy shards"
	} else {
		status.Message = "All systems healthy"
	}
	
	return status
}

// SetFreshnessConfig sets freshness SLO configuration for a table
func (s *WatermarkStore) SetFreshnessConfig(config *types.FreshnessConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	tableKey := config.Namespace + "." + config.Table
	s.freshnessConfigs[tableKey] = config
}

// CleanupStale removes stale watermark data
func (s *WatermarkStore) CleanupStale(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	now := time.Now()
	removed := 0
	
	// Remove stale service watermarks
	for id, sw := range s.serviceWatermarks {
		if now.Sub(sw.LastUpdate) > maxAge {
			delete(s.serviceWatermarks, id)
			removed++
		}
	}
	
	// Rebuild table watermarks after cleanup
	for tableKey := range s.tableWatermarks {
		s.updateTableWatermark(tableKey)
	}
	
	return removed
}

// Helper functions

func (s *WatermarkStore) isServiceHealthy(sw *types.ServiceWatermark) bool {
	// Service is healthy if:
	// 1. It has recent updates (< 30 seconds)
	// 2. Pending count is not too high
	// 3. Watermark is progressing
	
	if time.Since(sw.LastUpdate) > 30*time.Second {
		return false
	}
	
	if sw.PendingCount > sw.EventCount/2 && sw.EventCount > 1000 {
		return false
	}
	
	return true
}

func percentile(values []int64, p int) int64 {
	if len(values) == 0 {
		return 0
	}
	
	index := (len(values) - 1) * p / 100
	return values[index]
}

func splitTableKey(key string) []string {
	// Simple split - in production would be more robust
	parts := []string{}
	current := ""
	for _, ch := range key {
		if ch == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}