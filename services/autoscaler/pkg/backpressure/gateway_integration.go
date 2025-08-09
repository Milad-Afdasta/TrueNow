package backpressure

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// GatewayIntegration provides integration between the autoscaler's backpressure
// coordinator and the Gateway's backpressure mechanisms
type GatewayIntegration struct {
	coordinator *BackpressureCoordinator
	config      GatewayIntegrationConfig
	mu          sync.RWMutex
	
	// Metrics
	requestsHandled  uint64
	requestsRejected uint64
	
	// HTTP client for notifying Gateway
	httpClient *http.Client
}

// GatewayIntegrationConfig configures the gateway integration
type GatewayIntegrationConfig struct {
	// Gateway endpoints
	GatewayURLs []string `json:"gateway_urls"`
	
	// Notification settings
	NotifyOnLevelChange bool          `json:"notify_on_level_change"`
	NotificationTimeout time.Duration `json:"notification_timeout"`
	
	// Request handling
	EnableRequestFiltering bool `json:"enable_request_filtering"`
	EnableRateLimiting     bool `json:"enable_rate_limiting"`
	EnableCircuitBreaker   bool `json:"enable_circuit_breaker"`
}

// NewGatewayIntegration creates a new gateway integration
func NewGatewayIntegration(coordinator *BackpressureCoordinator, config GatewayIntegrationConfig) *GatewayIntegration {
	if config.NotificationTimeout == 0 {
		config.NotificationTimeout = 5 * time.Second
	}
	
	return &GatewayIntegration{
		coordinator: coordinator,
		config:      config,
		httpClient: &http.Client{
			Timeout: config.NotificationTimeout,
		},
	}
}

// HandleIncomingRequest determines if an incoming request should be processed
// based on current backpressure level
func (gi *GatewayIntegration) HandleIncomingRequest(ctx context.Context, priority int) (bool, error) {
	// Get current response strategy
	strategy := gi.coordinator.GetResponseStrategy()
	
	// Check circuit breaker first (before probabilistic dropping)
	if gi.config.EnableCircuitBreaker && strategy.CircuitBreaker {
		level := gi.coordinator.GetCurrentLevel()
		if level >= LevelHigh {
			// Circuit breaker is open for non-critical requests
			if priority < 3 { // Assuming priority 3 is critical
				gi.mu.Lock()
				gi.requestsRejected++
				gi.mu.Unlock()
				return false, fmt.Errorf("circuit breaker open")
			}
		}
	}
	
	// Check if we should drop the request (probabilistic)
	// Critical requests bypass probabilistic dropping
	if priority < 3 && gi.coordinator.ShouldDropRequest() {
		gi.mu.Lock()
		gi.requestsRejected++
		gi.mu.Unlock()
		
		return false, fmt.Errorf("request rejected due to backpressure")
	}
	
	gi.mu.Lock()
	gi.requestsHandled++
	gi.mu.Unlock()
	
	return true, nil
}

// GetAdjustedRateLimit returns the rate limit adjusted for backpressure
func (gi *GatewayIntegration) GetAdjustedRateLimit(baseLimit int) int {
	if !gi.config.EnableRateLimiting {
		return baseLimit
	}
	
	return gi.coordinator.GetRateLimit(baseLimit)
}

// GetQueueSizeLimit returns the queue size limit for the current backpressure level
func (gi *GatewayIntegration) GetQueueSizeLimit() int {
	strategy := gi.coordinator.GetResponseStrategy()
	if strategy.QueueSizeLimit > 0 {
		return strategy.QueueSizeLimit
	}
	return 1000 // Default
}

// GetRequestTimeout returns the timeout for the current backpressure level
func (gi *GatewayIntegration) GetRequestTimeout() time.Duration {
	strategy := gi.coordinator.GetResponseStrategy()
	if strategy.Timeout > 0 {
		return strategy.Timeout
	}
	return 10 * time.Second // Default
}

// NotifyGatewayLevelChange notifies all gateway instances of a backpressure level change
func (gi *GatewayIntegration) NotifyGatewayLevelChange(oldLevel, newLevel BackpressureLevel) error {
	if !gi.config.NotifyOnLevelChange || len(gi.config.GatewayURLs) == 0 {
		return nil
	}
	
	var wg sync.WaitGroup
	errChan := make(chan error, len(gi.config.GatewayURLs))
	
	for _, url := range gi.config.GatewayURLs {
		wg.Add(1)
		go func(gatewayURL string) {
			defer wg.Done()
			
			// Create notification request
			endpoint := fmt.Sprintf("%s/admin/backpressure/level", gatewayURL)
			req, err := http.NewRequest("POST", endpoint, nil)
			if err != nil {
				errChan <- fmt.Errorf("failed to create request for %s: %w", gatewayURL, err)
				return
			}
			
			// Add headers
			req.Header.Set("X-Backpressure-Level", newLevel.String())
			req.Header.Set("X-Previous-Level", oldLevel.String())
			
			// Send request
			resp, err := gi.httpClient.Do(req)
			if err != nil {
				errChan <- fmt.Errorf("failed to notify %s: %w", gatewayURL, err)
				return
			}
			defer resp.Body.Close()
			
			if resp.StatusCode != http.StatusOK {
				errChan <- fmt.Errorf("gateway %s returned status %d", gatewayURL, resp.StatusCode)
			}
		}(url)
	}
	
	wg.Wait()
	close(errChan)
	
	// Collect errors
	var errors []error
	for err := range errChan {
		if err != nil {
			errors = append(errors, err)
		}
	}
	
	if len(errors) > 0 {
		return fmt.Errorf("failed to notify %d gateways", len(errors))
	}
	
	return nil
}

// GetMetrics returns integration metrics
func (gi *GatewayIntegration) GetMetrics() GatewayIntegrationMetrics {
	gi.mu.RLock()
	defer gi.mu.RUnlock()
	
	return GatewayIntegrationMetrics{
		RequestsHandled:  gi.requestsHandled,
		RequestsRejected: gi.requestsRejected,
		RejectionRate:    float64(gi.requestsRejected) / float64(gi.requestsHandled+gi.requestsRejected) * 100,
		CurrentLevel:     gi.coordinator.GetCurrentLevel(),
	}
}

// GatewayIntegrationMetrics contains gateway integration metrics
type GatewayIntegrationMetrics struct {
	RequestsHandled  uint64            `json:"requests_handled"`
	RequestsRejected uint64            `json:"requests_rejected"`
	RejectionRate    float64           `json:"rejection_rate"`
	CurrentLevel     BackpressureLevel `json:"current_level"`
}

// CreateMiddleware creates HTTP middleware for backpressure handling
func (gi *GatewayIntegration) CreateMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract priority from request (could be header, query param, etc.)
			priority := extractPriority(r)
			
			// Check if request should be processed
			allowed, err := gi.HandleIncomingRequest(r.Context(), priority)
			if !allowed {
				// Return 503 Service Unavailable with backpressure info
				w.Header().Set("X-Backpressure-Level", gi.coordinator.GetCurrentLevel().String())
				w.Header().Set("Retry-After", "5") // Suggest retry after 5 seconds
				
				if err != nil {
					w.Header().Set("X-Rejection-Reason", err.Error())
				}
				
				http.Error(w, "Service temporarily unavailable due to high load", http.StatusServiceUnavailable)
				return
			}
			
			// Add backpressure headers to response
			w.Header().Set("X-Backpressure-Level", gi.coordinator.GetCurrentLevel().String())
			
			// Get adjusted timeout
			timeout := gi.GetRequestTimeout()
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			
			// Process request with timeout
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractPriority extracts priority from request
func extractPriority(r *http.Request) int {
	// Check header first
	if p := r.Header.Get("X-Priority"); p != "" {
		switch p {
		case "critical":
			return 3
		case "high":
			return 2
		case "normal":
			return 1
		case "low":
			return 0
		}
	}
	
	// Check if it's a health check or admin endpoint
	if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
		return 3 // Critical priority
	}
	
	// Default to normal priority
	return 1
}

// DefaultGatewayIntegrationConfig returns a default configuration
func DefaultGatewayIntegrationConfig() GatewayIntegrationConfig {
	return GatewayIntegrationConfig{
		GatewayURLs:            []string{"http://localhost:8080"},
		NotifyOnLevelChange:    true,
		NotificationTimeout:    5 * time.Second,
		EnableRequestFiltering: true,
		EnableRateLimiting:     true,
		EnableCircuitBreaker:   true,
	}
}