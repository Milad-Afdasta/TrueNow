package registry

import (
	"context"
	"fmt"
	"sync"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// InMemoryRegistry implements an in-memory service registry
type InMemoryRegistry struct {
	mu        sync.RWMutex
	instances map[string]*types.Instance // instanceID -> instance
	services  map[string]map[string]*types.Instance // serviceName -> instanceID -> instance
	watchers  map[string][]*watcherChannel // serviceName -> watchers
	config    RegistryConfig
	closed    bool
	stopCh    chan struct{}
}

type watcherChannel struct {
	ch     chan RegistryEvent
	closed bool
	mu     sync.Mutex
}

// NewInMemoryRegistry creates a new in-memory registry
func NewInMemoryRegistry(config RegistryConfig) *InMemoryRegistry {
	r := &InMemoryRegistry{
		instances: make(map[string]*types.Instance),
		services:  make(map[string]map[string]*types.Instance),
		watchers:  make(map[string][]*watcherChannel),
		config:    config,
		stopCh:    make(chan struct{}),
	}
	
	// Start cleanup goroutine if configured
	if config.CleanupInterval > 0 {
		go r.cleanupLoop()
	}
	
	return r
}

// Register registers a new service instance
func (r *InMemoryRegistry) Register(ctx context.Context, instance *types.Instance) error {
	if instance == nil {
		return fmt.Errorf("instance cannot be nil")
	}
	if instance.ID == "" {
		return fmt.Errorf("instance ID is required")
	}
	if instance.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return fmt.Errorf("registry is closed")
	}
	
	// Check if instance already exists
	if _, exists := r.instances[instance.ID]; exists {
		return fmt.Errorf("instance %s already registered", instance.ID)
	}
	
	// Store instance
	r.instances[instance.ID] = instance
	
	// Add to service map
	if r.services[instance.ServiceName] == nil {
		r.services[instance.ServiceName] = make(map[string]*types.Instance)
	}
	r.services[instance.ServiceName][instance.ID] = instance
	
	// Send event to watchers
	r.sendEvent(RegistryEvent{
		Type:        EventRegistered,
		Instance:    instance,
		Timestamp:   time.Now(),
		ServiceName: instance.ServiceName,
	})
	
	return nil
}

// Deregister removes a service instance from the registry
func (r *InMemoryRegistry) Deregister(ctx context.Context, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return fmt.Errorf("registry is closed")
	}
	
	instance, exists := r.instances[instanceID]
	if !exists {
		return types.ErrInstanceNotFound
	}
	
	// Remove from instances map
	delete(r.instances, instanceID)
	
	// Remove from services map
	if serviceInstances, ok := r.services[instance.ServiceName]; ok {
		delete(serviceInstances, instanceID)
		if len(serviceInstances) == 0 {
			delete(r.services, instance.ServiceName)
		}
	}
	
	// Send event to watchers
	r.sendEvent(RegistryEvent{
		Type:        EventDeregistered,
		Instance:    instance,
		Timestamp:   time.Now(),
		ServiceName: instance.ServiceName,
	})
	
	return nil
}

// Update updates an existing instance's information
func (r *InMemoryRegistry) Update(ctx context.Context, instance *types.Instance) error {
	if instance == nil || instance.ID == "" {
		return fmt.Errorf("invalid instance")
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return fmt.Errorf("registry is closed")
	}
	
	existing, exists := r.instances[instance.ID]
	if !exists {
		return types.ErrInstanceNotFound
	}
	
	// Update instance
	r.instances[instance.ID] = instance
	
	// Update in services map
	if existing.ServiceName != instance.ServiceName {
		// Service name changed, move to new service
		if serviceInstances, ok := r.services[existing.ServiceName]; ok {
			delete(serviceInstances, instance.ID)
			if len(serviceInstances) == 0 {
				delete(r.services, existing.ServiceName)
			}
		}
		
		if r.services[instance.ServiceName] == nil {
			r.services[instance.ServiceName] = make(map[string]*types.Instance)
		}
		r.services[instance.ServiceName][instance.ID] = instance
	} else {
		r.services[instance.ServiceName][instance.ID] = instance
	}
	
	// Send event to watchers
	r.sendEvent(RegistryEvent{
		Type:        EventUpdated,
		Instance:    instance,
		Timestamp:   time.Now(),
		ServiceName: instance.ServiceName,
	})
	
	return nil
}

// Get retrieves a specific instance by ID
func (r *InMemoryRegistry) Get(ctx context.Context, instanceID string) (*types.Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	instance, exists := r.instances[instanceID]
	if !exists {
		return nil, types.ErrInstanceNotFound
	}
	
	// Return a copy to prevent external modifications
	copy := *instance
	return &copy, nil
}

// GetByService retrieves all instances for a specific service
func (r *InMemoryRegistry) GetByService(ctx context.Context, serviceName string) ([]*types.Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	serviceInstances, exists := r.services[serviceName]
	if !exists {
		return []*types.Instance{}, nil
	}
	
	instances := make([]*types.Instance, 0, len(serviceInstances))
	for _, instance := range serviceInstances {
		copy := *instance
		instances = append(instances, &copy)
	}
	
	return instances, nil
}

// GetAll retrieves all registered instances
func (r *InMemoryRegistry) GetAll(ctx context.Context) ([]*types.Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	instances := make([]*types.Instance, 0, len(r.instances))
	for _, instance := range r.instances {
		copy := *instance
		instances = append(instances, &copy)
	}
	
	return instances, nil
}

// GetHealthy retrieves all healthy instances for a service
func (r *InMemoryRegistry) GetHealthy(ctx context.Context, serviceName string) ([]*types.Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	serviceInstances, exists := r.services[serviceName]
	if !exists {
		return []*types.Instance{}, nil
	}
	
	healthy := make([]*types.Instance, 0)
	for _, instance := range serviceInstances {
		if instance.IsHealthy() {
			copy := *instance
			healthy = append(healthy, &copy)
		}
	}
	
	return healthy, nil
}

// UpdateStatus updates the status of an instance
func (r *InMemoryRegistry) UpdateStatus(ctx context.Context, instanceID string, status types.InstanceStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return fmt.Errorf("registry is closed")
	}
	
	instance, exists := r.instances[instanceID]
	if !exists {
		return types.ErrInstanceNotFound
	}
	
	oldStatus := instance.Status
	instance.Status = status
	instance.LastHealthCheck = time.Now()
	
	// Send event to watchers
	r.sendEvent(RegistryEvent{
		Type:        EventStatusChange,
		Instance:    instance,
		OldStatus:   oldStatus,
		NewStatus:   status,
		Timestamp:   time.Now(),
		ServiceName: instance.ServiceName,
	})
	
	return nil
}

// UpdateMetrics updates the metrics for an instance
func (r *InMemoryRegistry) UpdateMetrics(ctx context.Context, instanceID string, metrics map[string]float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return fmt.Errorf("registry is closed")
	}
	
	instance, exists := r.instances[instanceID]
	if !exists {
		return types.ErrInstanceNotFound
	}
	
	instance.Metrics = metrics
	instance.LastHealthCheck = time.Now()
	
	return nil
}

// GetServiceState gets the current state of a service
func (r *InMemoryRegistry) GetServiceState(ctx context.Context, serviceName string) (*types.ServiceState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	serviceInstances, exists := r.services[serviceName]
	if !exists {
		return &types.ServiceState{
			ServiceName:      serviceName,
			CurrentInstances: 0,
			HealthyInstances: 0,
			Instances:        make(map[string]*types.Instance),
		}, nil
	}
	
	state := &types.ServiceState{
		ServiceName:      serviceName,
		CurrentInstances: len(serviceInstances),
		Instances:        make(map[string]*types.Instance),
		CurrentMetrics:   make(map[string]float64),
	}
	
	// Count healthy instances and copy instance map
	for id, instance := range serviceInstances {
		if instance.IsHealthy() {
			state.HealthyInstances++
		}
		copy := *instance
		state.Instances[id] = &copy
		
		// Aggregate metrics
		for metricName, value := range instance.Metrics {
			state.CurrentMetrics[metricName] += value
		}
	}
	
	// Average the metrics
	if state.CurrentInstances > 0 {
		for metricName := range state.CurrentMetrics {
			state.CurrentMetrics[metricName] /= float64(state.CurrentInstances)
		}
	}
	
	return state, nil
}

// Watch watches for changes to service instances
func (r *InMemoryRegistry) Watch(ctx context.Context, serviceName string) (<-chan RegistryEvent, error) {
	if !r.config.EnableWatching {
		return nil, fmt.Errorf("watching is disabled")
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return nil, fmt.Errorf("registry is closed")
	}
	
	watcher := &watcherChannel{
		ch:     make(chan RegistryEvent, 100),
		closed: false,
	}
	r.watchers[serviceName] = append(r.watchers[serviceName], watcher)
	
	// Clean up watcher when context is done
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		defer r.mu.Unlock()
		
		// Remove this watcher
		if watchers, ok := r.watchers[serviceName]; ok {
			for i, w := range watchers {
				if w == watcher {
					r.watchers[serviceName] = append(watchers[:i], watchers[i+1:]...)
					w.mu.Lock()
					if !w.closed {
						close(w.ch)
						w.closed = true
					}
					w.mu.Unlock()
					break
				}
			}
		}
	}()
	
	return watcher.ch, nil
}

// Close closes the registry
func (r *InMemoryRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.closed {
		return nil
	}
	
	r.closed = true
	close(r.stopCh)
	
	// Close all watcher channels (they may have been closed by context cancellation)
	for serviceName, watchers := range r.watchers {
		for _, watcher := range watchers {
			watcher.mu.Lock()
			if !watcher.closed {
				close(watcher.ch)
				watcher.closed = true
			}
			watcher.mu.Unlock()
		}
		delete(r.watchers, serviceName)
	}
	
	return nil
}

// sendEvent sends an event to all relevant watchers
func (r *InMemoryRegistry) sendEvent(event RegistryEvent) {
	// Send to service-specific watchers
	if watchers, ok := r.watchers[event.ServiceName]; ok {
		for _, watcher := range watchers {
			watcher.mu.Lock()
			if !watcher.closed {
				select {
				case watcher.ch <- event:
				default:
					// Channel full, skip
				}
			}
			watcher.mu.Unlock()
		}
	}
	
	// Send to global watchers (watching "*")
	if watchers, ok := r.watchers["*"]; ok {
		for _, watcher := range watchers {
			watcher.mu.Lock()
			if !watcher.closed {
				select {
				case watcher.ch <- event:
				default:
					// Channel full, skip
				}
			}
			watcher.mu.Unlock()
		}
	}
}

// cleanupLoop periodically cleans up stale instances
func (r *InMemoryRegistry) cleanupLoop() {
	ticker := time.NewTicker(r.config.CleanupInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			r.cleanup()
		case <-r.stopCh:
			return
		}
	}
}

// cleanup removes stale instances
func (r *InMemoryRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	now := time.Now()
	staleThreshold := now.Add(-r.config.TTL * 3) // 3x TTL for staleness
	
	for _, instance := range r.instances {
		// Check if instance hasn't been updated recently
		if instance.LastHealthCheck.Before(staleThreshold) {
			// Mark as unhealthy first
			if instance.Status != types.StatusUnhealthy {
				oldStatus := instance.Status
				instance.Status = types.StatusUnhealthy
				r.sendEvent(RegistryEvent{
					Type:        EventStatusChange,
					Instance:    instance,
					OldStatus:   oldStatus,
					NewStatus:   types.StatusUnhealthy,
					Timestamp:   now,
					ServiceName: instance.ServiceName,
				})
			}
		}
	}
}