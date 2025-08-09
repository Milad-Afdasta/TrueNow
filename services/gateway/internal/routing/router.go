package routing

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/discovery"
	"github.com/valyala/fasthttp"
	log "github.com/sirupsen/logrus"
)

// Router routes requests to backend services using service discovery
type Router struct {
	discovery    discovery.ServiceDiscovery
	httpClient   *fasthttp.Client
	
	// Circuit breaker
	failures     map[string]*uint32
	failureLimit uint32
	
	// Stats
	requestCount uint64
	errorCount   uint64
	
	mu sync.RWMutex
}

// NewRouter creates a new router with service discovery
func NewRouter(discovery discovery.ServiceDiscovery) *Router {
	return &Router{
		discovery: discovery,
		httpClient: &fasthttp.Client{
			MaxConnsPerHost:     1000,
			MaxIdleConnDuration: 10 * time.Second,
			ReadTimeout:         5 * time.Second,
			WriteTimeout:        5 * time.Second,
		},
		failures:     make(map[string]*uint32),
		failureLimit: 5,
	}
}

// RouteToService routes a request to a healthy instance of the specified service
func (r *Router) RouteToService(ctx *fasthttp.RequestCtx, serviceName string) error {
	atomic.AddUint64(&r.requestCount, 1)
	
	// Get a healthy instance
	instance, err := r.discovery.GetHealthyInstance(serviceName)
	if err != nil {
		atomic.AddUint64(&r.errorCount, 1)
		return fmt.Errorf("no healthy instances available: %w", err)
	}
	
	// Check circuit breaker
	if r.isCircuitOpen(instance.ID) {
		atomic.AddUint64(&r.errorCount, 1)
		return fmt.Errorf("circuit breaker open for instance %s", instance.ID)
	}
	
	// Build target URL
	targetURL := fmt.Sprintf("http://%s:%d%s", instance.Host, instance.Port, string(ctx.Path()))
	
	// Create request
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	
	// Copy request
	ctx.Request.CopyTo(req)
	req.SetRequestURI(targetURL)
	
	// Forward request
	startTime := time.Now()
	err = r.httpClient.Do(req, resp)
	latency := time.Since(startTime)
	
	if err != nil {
		r.recordFailure(instance.ID)
		atomic.AddUint64(&r.errorCount, 1)
		
		log.WithFields(log.Fields{
			"service":  serviceName,
			"instance": instance.ID,
			"error":    err.Error(),
			"latency":  latency,
		}).Error("Failed to route request")
		
		return fmt.Errorf("failed to forward request: %w", err)
	}
	
	// Reset failures on success
	r.resetFailures(instance.ID)
	
	// Copy response
	resp.CopyTo(&ctx.Response)
	
	log.WithFields(log.Fields{
		"service":     serviceName,
		"instance":    instance.ID,
		"status_code": resp.StatusCode(),
		"latency":     latency,
	}).Debug("Request routed successfully")
	
	return nil
}

// RouteWithRetry routes a request with retry logic
func (r *Router) RouteWithRetry(ctx *fasthttp.RequestCtx, serviceName string, maxRetries int) error {
	var lastErr error
	
	for i := 0; i <= maxRetries; i++ {
		err := r.RouteToService(ctx, serviceName)
		if err == nil {
			return nil
		}
		
		lastErr = err
		
		if i < maxRetries {
			// Exponential backoff
			backoff := time.Duration(1<<uint(i)) * 100 * time.Millisecond
			if backoff > 2*time.Second {
				backoff = 2 * time.Second
			}
			time.Sleep(backoff)
		}
	}
	
	return fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

// isCircuitOpen checks if the circuit breaker is open for an instance
func (r *Router) isCircuitOpen(instanceID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	if failures, exists := r.failures[instanceID]; exists {
		return atomic.LoadUint32(failures) >= r.failureLimit
	}
	
	return false
}

// recordFailure records a failure for an instance
func (r *Router) recordFailure(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if _, exists := r.failures[instanceID]; !exists {
		var count uint32
		r.failures[instanceID] = &count
	}
	
	atomic.AddUint32(r.failures[instanceID], 1)
}

// resetFailures resets failure count for an instance
func (r *Router) resetFailures(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if failures, exists := r.failures[instanceID]; exists {
		atomic.StoreUint32(failures, 0)
	}
}

// GetStats returns routing statistics
func (r *Router) GetStats() (requests uint64, errors uint64) {
	return atomic.LoadUint64(&r.requestCount), atomic.LoadUint64(&r.errorCount)
}

// MultiServiceRouter routes to multiple backend services
type MultiServiceRouter struct {
	router *Router
	routes map[string]string // path prefix -> service name
	mu     sync.RWMutex
}

// NewMultiServiceRouter creates a router that can route to multiple services
func NewMultiServiceRouter(discovery discovery.ServiceDiscovery) *MultiServiceRouter {
	return &MultiServiceRouter{
		router: NewRouter(discovery),
		routes: map[string]string{
			"/ingest":  "stream-ingester",
			"/query":   "query-api",
			"/metrics": "hot-tier",
			"/events":  "stream-ingester",
		},
	}
}

// Route routes a request based on path prefix
func (mr *MultiServiceRouter) Route(ctx *fasthttp.RequestCtx) error {
	path := string(ctx.Path())
	
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	
	// Find matching route
	for prefix, serviceName := range mr.routes {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return mr.router.RouteWithRetry(ctx, serviceName, 2)
		}
	}
	
	// Default to stream-ingester for unmatched paths
	return mr.router.RouteWithRetry(ctx, "stream-ingester", 2)
}

// AddRoute adds a new routing rule
func (mr *MultiServiceRouter) AddRoute(prefix, serviceName string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	
	mr.routes[prefix] = serviceName
}

// RemoveRoute removes a routing rule
func (mr *MultiServiceRouter) RemoveRoute(prefix string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	
	delete(mr.routes, prefix)
}

// GetRouter returns the underlying router
func (mr *MultiServiceRouter) GetRouter() *Router {
	return mr.router
}