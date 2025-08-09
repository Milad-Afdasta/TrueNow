package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/store"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/pkg/types"
)

// ServiceEndpoint represents a service to collect watermarks from
type ServiceEndpoint struct {
	ServiceID   string
	ServiceType string
	URL         string
	Namespace   string
	Table       string
	ShardID     int32
}

// WatermarkAggregator collects watermarks from multiple services
type WatermarkAggregator struct {
	store     *store.WatermarkStore
	endpoints []ServiceEndpoint
	client    *http.Client
	interval  time.Duration
	logger    *logrus.Logger
	
	mu       sync.RWMutex
	running  bool
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewWatermarkAggregator creates a new aggregator
func NewWatermarkAggregator(store *store.WatermarkStore, logger *logrus.Logger) *WatermarkAggregator {
	return &WatermarkAggregator{
		store:     store,
		endpoints: []ServiceEndpoint{},
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		interval: 5 * time.Second,
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

// AddEndpoint adds a service endpoint to collect from
func (a *WatermarkAggregator) AddEndpoint(endpoint ServiceEndpoint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	a.endpoints = append(a.endpoints, endpoint)
	a.logger.WithFields(logrus.Fields{
		"service_id":   endpoint.ServiceID,
		"service_type": endpoint.ServiceType,
		"url":          endpoint.URL,
	}).Info("Added watermark collection endpoint")
}

// Start begins collecting watermarks
func (a *WatermarkAggregator) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("aggregator already running")
	}
	a.running = true
	a.mu.Unlock()
	
	a.logger.Info("Starting watermark aggregator")
	
	// Start collection goroutine
	a.wg.Add(1)
	go a.collectLoop(ctx)
	
	// Start cleanup goroutine
	a.wg.Add(1)
	go a.cleanupLoop(ctx)
	
	return nil
}

// Stop stops the aggregator
func (a *WatermarkAggregator) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	a.mu.Unlock()
	
	a.logger.Info("Stopping watermark aggregator")
	close(a.stopChan)
	a.wg.Wait()
}

// collectLoop periodically collects watermarks from all endpoints
func (a *WatermarkAggregator) collectLoop(ctx context.Context) {
	defer a.wg.Done()
	
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	
	// Collect immediately on start
	a.collectAll(ctx)
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopChan:
			return
		case <-ticker.C:
			a.collectAll(ctx)
		}
	}
}

// collectAll collects watermarks from all endpoints
func (a *WatermarkAggregator) collectAll(ctx context.Context) {
	a.mu.RLock()
	endpoints := make([]ServiceEndpoint, len(a.endpoints))
	copy(endpoints, a.endpoints)
	a.mu.RUnlock()
	
	var wg sync.WaitGroup
	for _, endpoint := range endpoints {
		wg.Add(1)
		go func(ep ServiceEndpoint) {
			defer wg.Done()
			a.collectFromEndpoint(ctx, ep)
		}(endpoint)
	}
	
	// Wait for all collections to complete with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// All collections completed
	case <-time.After(10 * time.Second):
		a.logger.Warn("Watermark collection timeout")
	}
}

// collectFromEndpoint collects watermark from a single endpoint
func (a *WatermarkAggregator) collectFromEndpoint(ctx context.Context, endpoint ServiceEndpoint) {
	logger := a.logger.WithFields(logrus.Fields{
		"service_id":   endpoint.ServiceID,
		"service_type": endpoint.ServiceType,
	})
	
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.URL+"/watermark", nil)
	if err != nil {
		logger.WithError(err).Error("Failed to create request")
		return
	}
	
	// Make request
	resp, err := a.client.Do(req)
	if err != nil {
		logger.WithError(err).Debug("Failed to fetch watermark")
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		logger.WithField("status", resp.StatusCode).Debug("Non-OK status from endpoint")
		return
	}
	
	// Parse response based on service type
	var update *types.WatermarkUpdate
	
	switch endpoint.ServiceType {
	case "gateway":
		update = a.parseGatewayResponse(resp, endpoint)
	case "stream-ingester":
		update = a.parseStreamIngesterResponse(resp, endpoint)
	case "hot-tier":
		update = a.parseHotTierResponse(resp, endpoint)
	default:
		update = a.parseGenericResponse(resp, endpoint)
	}
	
	if update != nil {
		a.store.UpdateServiceWatermark(update)
		logger.WithFields(logrus.Fields{
			"event_time":      update.EventTime,
			"processing_time": update.ProcessingTime,
			"event_count":     update.EventCount,
		}).Debug("Updated watermark")
	}
}

// parseGatewayResponse parses watermark response from gateway service
func (a *WatermarkAggregator) parseGatewayResponse(resp *http.Response, endpoint ServiceEndpoint) *types.WatermarkUpdate {
	var data struct {
		MinWatermark   int64 `json:"min_watermark"`
		MaxWatermark   int64 `json:"max_watermark"`
		ProcessingTime int64 `json:"processing_time"`
		EventTime      int64 `json:"event_time"`
		UpdateCount    int64 `json:"update_count"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		a.logger.WithError(err).Error("Failed to decode gateway response")
		return nil
	}
	
	return &types.WatermarkUpdate{
		ServiceID:      endpoint.ServiceID,
		ServiceType:    endpoint.ServiceType,
		Namespace:      endpoint.Namespace,
		Table:          endpoint.Table,
		ShardID:        endpoint.ShardID,
		EventTime:      data.MaxWatermark,
		ProcessingTime: data.ProcessingTime,
		EventCount:     data.UpdateCount,
		PendingCount:   0,
		Timestamp:      time.Now(),
	}
}

// parseStreamIngesterResponse parses watermark response from stream ingester
func (a *WatermarkAggregator) parseStreamIngesterResponse(resp *http.Response, endpoint ServiceEndpoint) *types.WatermarkUpdate {
	var data struct {
		LatestOffset   int64 `json:"latest_offset"`
		ProcessedCount int64 `json:"processed_count"`
		PendingCount   int64 `json:"pending_count"`
		LastEventTime  int64 `json:"last_event_time"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		a.logger.WithError(err).Error("Failed to decode stream ingester response")
		return nil
	}
	
	return &types.WatermarkUpdate{
		ServiceID:      endpoint.ServiceID,
		ServiceType:    endpoint.ServiceType,
		Namespace:      endpoint.Namespace,
		Table:          endpoint.Table,
		ShardID:        endpoint.ShardID,
		EventTime:      data.LastEventTime,
		ProcessingTime: time.Now().UnixMilli(),
		EventCount:     data.ProcessedCount,
		PendingCount:   data.PendingCount,
		Timestamp:      time.Now(),
	}
}

// parseHotTierResponse parses watermark response from hot tier
func (a *WatermarkAggregator) parseHotTierResponse(resp *http.Response, endpoint ServiceEndpoint) *types.WatermarkUpdate {
	var data struct {
		ShardID        int32 `json:"shard_id"`
		EventCount     int64 `json:"event_count"`
		OldestEvent    int64 `json:"oldest_event_ms"`
		NewestEvent    int64 `json:"newest_event_ms"`
		MemoryUsage    int64 `json:"memory_usage_bytes"`
		LastUpdate     int64 `json:"last_update_ms"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		a.logger.WithError(err).Error("Failed to decode hot tier response")
		return nil
	}
	
	return &types.WatermarkUpdate{
		ServiceID:      endpoint.ServiceID,
		ServiceType:    endpoint.ServiceType,
		Namespace:      endpoint.Namespace,
		Table:          endpoint.Table,
		ShardID:        data.ShardID,
		EventTime:      data.NewestEvent,
		ProcessingTime: data.LastUpdate,
		EventCount:     data.EventCount,
		PendingCount:   0,
		Timestamp:      time.Now(),
	}
}

// parseGenericResponse parses a generic watermark response
func (a *WatermarkAggregator) parseGenericResponse(resp *http.Response, endpoint ServiceEndpoint) *types.WatermarkUpdate {
	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		a.logger.WithError(err).Error("Failed to decode generic response")
		return nil
	}
	
	update := &types.WatermarkUpdate{
		ServiceID:      endpoint.ServiceID,
		ServiceType:    endpoint.ServiceType,
		Namespace:      endpoint.Namespace,
		Table:          endpoint.Table,
		ShardID:        endpoint.ShardID,
		ProcessingTime: time.Now().UnixMilli(),
		Timestamp:      time.Now(),
	}
	
	// Try to extract common fields
	if val, ok := data["watermark"].(float64); ok {
		update.EventTime = int64(val)
	} else if val, ok := data["event_time"].(float64); ok {
		update.EventTime = int64(val)
	}
	
	if val, ok := data["event_count"].(float64); ok {
		update.EventCount = int64(val)
	}
	
	if val, ok := data["pending_count"].(float64); ok {
		update.PendingCount = int64(val)
	}
	
	return update
}

// cleanupLoop periodically cleans up stale data
func (a *WatermarkAggregator) cleanupLoop(ctx context.Context) {
	defer a.wg.Done()
	
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopChan:
			return
		case <-ticker.C:
			removed := a.store.CleanupStale(10 * time.Minute)
			if removed > 0 {
				a.logger.WithField("removed", removed).Info("Cleaned up stale watermarks")
			}
		}
	}
}

// SetCollectionInterval sets the collection interval
func (a *WatermarkAggregator) SetCollectionInterval(interval time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.interval = interval
}

// DiscoverEndpoints discovers service endpoints from control plane
func (a *WatermarkAggregator) DiscoverEndpoints(controlPlaneURL string) error {
	// In production, this would query the control plane for active services
	// For now, we'll use hardcoded endpoints
	
	// Gateway
	a.AddEndpoint(ServiceEndpoint{
		ServiceID:   "gateway-1",
		ServiceType: "gateway",
		URL:         "http://localhost:8088",
		Namespace:   "default",
		Table:       "events",
		ShardID:     -1, // Gateway doesn't have shards
	})
	
	// Stream ingesters (simulated multiple instances)
	for i := 0; i < 3; i++ {
		a.AddEndpoint(ServiceEndpoint{
			ServiceID:   fmt.Sprintf("stream-ingester-%d", i),
			ServiceType: "stream-ingester",
			URL:         fmt.Sprintf("http://localhost:%d", 8100+i),
			Namespace:   "default",
			Table:       "events",
			ShardID:     int32(i),
		})
	}
	
	// Hot tier shards (simulated multiple shards)
	for i := 0; i < 4; i++ {
		a.AddEndpoint(ServiceEndpoint{
			ServiceID:   fmt.Sprintf("hot-tier-shard-%d", i),
			ServiceType: "hot-tier",
			URL:         fmt.Sprintf("http://localhost:%d", 9090+i),
			Namespace:   "default",
			Table:       "events",
			ShardID:     int32(i),
		})
	}
	
	a.logger.WithField("endpoints", len(a.endpoints)).Info("Discovered service endpoints")
	return nil
}