package registry

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// HTTPHealthChecker implements health checking via HTTP endpoints
type HTTPHealthChecker struct {
	config     RegistryConfig
	client     *http.Client
	registry   Registry
	stopCh     chan struct{}
	wg         sync.WaitGroup
	monitoring bool
	mu         sync.Mutex
}

// NewHTTPHealthChecker creates a new HTTP health checker
func NewHTTPHealthChecker(config RegistryConfig) *HTTPHealthChecker {
	return &HTTPHealthChecker{
		config: config,
		client: &http.Client{
			Timeout: config.HealthCheckTimeout,
		},
		stopCh: make(chan struct{}),
	}
}

// CheckHealth checks the health of an instance via HTTP
func (h *HTTPHealthChecker) CheckHealth(ctx context.Context, instance *types.Instance) error {
	if instance.Status == types.StatusStopping || instance.Status == types.StatusStopped {
		return nil // Don't check instances that are stopping/stopped
	}
	
	// Construct health check URL
	healthURL := fmt.Sprintf("http://%s:%d%s", instance.Host, instance.Port, h.getHealthEndpoint(instance))
	
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}
	
	// Perform health check
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	
	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy status code: %d", resp.StatusCode)
	}
	
	return nil
}

// StartMonitoring starts health monitoring for all instances
func (h *HTTPHealthChecker) StartMonitoring(ctx context.Context, registry Registry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if h.monitoring {
		return fmt.Errorf("health monitoring already started")
	}
	
	h.registry = registry
	h.monitoring = true
	
	// Start monitoring goroutine
	h.wg.Add(1)
	go h.monitorLoop(ctx)
	
	return nil
}

// StopMonitoring stops health monitoring
func (h *HTTPHealthChecker) StopMonitoring() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if !h.monitoring {
		return nil
	}
	
	h.monitoring = false
	close(h.stopCh)
	h.wg.Wait()
	
	return nil
}

// monitorLoop is the main monitoring loop
func (h *HTTPHealthChecker) monitorLoop(ctx context.Context) {
	defer h.wg.Done()
	
	ticker := time.NewTicker(h.config.HealthCheckInterval)
	defer ticker.Stop()
	
	// Perform initial health check immediately
	h.checkAllInstances(ctx)
	
	for {
		select {
		case <-ticker.C:
			h.checkAllInstances(ctx)
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		}
	}
}

// checkAllInstances checks the health of all registered instances
func (h *HTTPHealthChecker) checkAllInstances(ctx context.Context) {
	instances, err := h.registry.GetAll(ctx)
	if err != nil {
		return
	}
	
	// Check instances concurrently
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent checks
	
	for _, instance := range instances {
		wg.Add(1)
		go func(inst *types.Instance) {
			defer wg.Done()
			
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			h.checkInstance(ctx, inst)
		}(instance)
	}
	
	wg.Wait()
}

// checkInstance checks a single instance and updates its status
func (h *HTTPHealthChecker) checkInstance(ctx context.Context, instance *types.Instance) {
	// Create context with timeout for this specific check
	checkCtx, cancel := context.WithTimeout(ctx, h.config.HealthCheckTimeout)
	defer cancel()
	
	// Perform health check with retries
	var lastErr error
	for i := 0; i <= h.config.MaxRetries; i++ {
		if i > 0 {
			// Wait before retry
			select {
			case <-time.After(time.Second):
			case <-checkCtx.Done():
				break
			}
		}
		
		err := h.CheckHealth(checkCtx, instance)
		if err == nil {
			// Health check passed
			if instance.Status != types.StatusRunning {
				h.registry.UpdateStatus(ctx, instance.ID, types.StatusRunning)
			}
			return
		}
		lastErr = err
	}
	
	// Health check failed after retries
	if instance.Status == types.StatusRunning || instance.Status == types.StatusStarting {
		h.registry.UpdateStatus(ctx, instance.ID, types.StatusUnhealthy)
	}
	
	// Log error for debugging (in production, use proper logging)
	_ = lastErr
}

// getHealthEndpoint gets the health check endpoint for an instance
func (h *HTTPHealthChecker) getHealthEndpoint(instance *types.Instance) string {
	// Could be customized per service, for now use a default
	return "/health"
}

// SimpleHealthChecker implements a simple health checker for testing
type SimpleHealthChecker struct {
	healthy map[string]bool
	mu      sync.RWMutex
}

// NewSimpleHealthChecker creates a new simple health checker
func NewSimpleHealthChecker() *SimpleHealthChecker {
	return &SimpleHealthChecker{
		healthy: make(map[string]bool),
	}
}

// CheckHealth checks the health of an instance (always returns healthy for testing)
func (s *SimpleHealthChecker) CheckHealth(ctx context.Context, instance *types.Instance) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if healthy, ok := s.healthy[instance.ID]; ok && !healthy {
		return fmt.Errorf("instance marked as unhealthy")
	}
	
	return nil
}

// StartMonitoring starts health monitoring (no-op for simple checker)
func (s *SimpleHealthChecker) StartMonitoring(ctx context.Context, registry Registry) error {
	// No-op for simple checker
	return nil
}

// StopMonitoring stops health monitoring (no-op for simple checker)
func (s *SimpleHealthChecker) StopMonitoring() error {
	// No-op for simple checker
	return nil
}

// SetHealthy sets the health status of an instance for testing
func (s *SimpleHealthChecker) SetHealthy(instanceID string, healthy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthy[instanceID] = healthy
}