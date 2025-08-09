package discovery

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
	
	log "github.com/sirupsen/logrus"
)

// ServiceInstance represents a discovered service instance
type ServiceInstance struct {
	ID       string
	Host     string
	Port     int
	Healthy  bool
	LastSeen time.Time
}

// ServiceDiscovery provides service discovery capabilities
type ServiceDiscovery interface {
	// GetInstances returns all instances of a service
	GetInstances(serviceName string) ([]*ServiceInstance, error)
	
	// GetHealthyInstance returns a healthy instance using load balancing
	GetHealthyInstance(serviceName string) (*ServiceInstance, error)
	
	// RegisterInstance registers a new instance
	RegisterInstance(serviceName string, instance *ServiceInstance) error
	
	// DeregisterInstance removes an instance
	DeregisterInstance(serviceName string, instanceID string) error
	
	// UpdateHealth updates the health status of an instance
	UpdateHealth(serviceName string, instanceID string, healthy bool) error
	
	// Watch watches for changes to service instances
	Watch(ctx context.Context, serviceName string) (<-chan []*ServiceInstance, error)
	
	// Close closes the service discovery
	Close() error
}

// LocalDiscovery implements local service discovery with round-robin load balancing
type LocalDiscovery struct {
	instances   map[string]map[string]*ServiceInstance // service -> instanceID -> instance
	roundRobin  map[string]*uint64                      // service -> counter for round-robin
	watchers    map[string][]chan []*ServiceInstance   // service -> watchers
	mu          sync.RWMutex
	closed      bool
}

// NewLocalDiscovery creates a new local service discovery
func NewLocalDiscovery() *LocalDiscovery {
	return &LocalDiscovery{
		instances:  make(map[string]map[string]*ServiceInstance),
		roundRobin: make(map[string]*uint64),
		watchers:   make(map[string][]chan []*ServiceInstance),
	}
}

// GetInstances returns all instances of a service
func (d *LocalDiscovery) GetInstances(serviceName string) ([]*ServiceInstance, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	if d.closed {
		return nil, fmt.Errorf("discovery is closed")
	}
	
	serviceInstances, exists := d.instances[serviceName]
	if !exists {
		return []*ServiceInstance{}, nil
	}
	
	instances := make([]*ServiceInstance, 0, len(serviceInstances))
	for _, inst := range serviceInstances {
		instances = append(instances, inst)
	}
	
	return instances, nil
}

// GetHealthyInstance returns a healthy instance using round-robin load balancing
func (d *LocalDiscovery) GetHealthyInstance(serviceName string) (*ServiceInstance, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	if d.closed {
		return nil, fmt.Errorf("discovery is closed")
	}
	
	serviceInstances, exists := d.instances[serviceName]
	if !exists || len(serviceInstances) == 0 {
		return nil, fmt.Errorf("no instances available for service %s", serviceName)
	}
	
	// Collect healthy instances
	healthyInstances := make([]*ServiceInstance, 0, len(serviceInstances))
	for _, inst := range serviceInstances {
		if inst.Healthy {
			healthyInstances = append(healthyInstances, inst)
		}
	}
	
	if len(healthyInstances) == 0 {
		return nil, fmt.Errorf("no healthy instances available for service %s", serviceName)
	}
	
	// Round-robin selection
	if _, exists := d.roundRobin[serviceName]; !exists {
		var counter uint64
		d.roundRobin[serviceName] = &counter
	}
	
	index := atomic.AddUint64(d.roundRobin[serviceName], 1) % uint64(len(healthyInstances))
	return healthyInstances[index], nil
}

// RegisterInstance registers a new instance
func (d *LocalDiscovery) RegisterInstance(serviceName string, instance *ServiceInstance) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.closed {
		return fmt.Errorf("discovery is closed")
	}
	
	if _, exists := d.instances[serviceName]; !exists {
		d.instances[serviceName] = make(map[string]*ServiceInstance)
	}
	
	instance.LastSeen = time.Now()
	d.instances[serviceName][instance.ID] = instance
	
	// Notify watchers
	d.notifyWatchers(serviceName)
	
	log.WithFields(log.Fields{
		"service":  serviceName,
		"instance": instance.ID,
		"host":     instance.Host,
		"port":     instance.Port,
	}).Info("Instance registered")
	
	return nil
}

// DeregisterInstance removes an instance
func (d *LocalDiscovery) DeregisterInstance(serviceName string, instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.closed {
		return fmt.Errorf("discovery is closed")
	}
	
	if serviceInstances, exists := d.instances[serviceName]; exists {
		delete(serviceInstances, instanceID)
		
		// Notify watchers
		d.notifyWatchers(serviceName)
		
		log.WithFields(log.Fields{
			"service":  serviceName,
			"instance": instanceID,
		}).Info("Instance deregistered")
	}
	
	return nil
}

// UpdateHealth updates the health status of an instance
func (d *LocalDiscovery) UpdateHealth(serviceName string, instanceID string, healthy bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.closed {
		return fmt.Errorf("discovery is closed")
	}
	
	if serviceInstances, exists := d.instances[serviceName]; exists {
		if instance, exists := serviceInstances[instanceID]; exists {
			instance.Healthy = healthy
			instance.LastSeen = time.Now()
			
			// Notify watchers
			d.notifyWatchers(serviceName)
			
			log.WithFields(log.Fields{
				"service":  serviceName,
				"instance": instanceID,
				"healthy":  healthy,
			}).Debug("Instance health updated")
		}
	}
	
	return nil
}

// Watch watches for changes to service instances
func (d *LocalDiscovery) Watch(ctx context.Context, serviceName string) (<-chan []*ServiceInstance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.closed {
		return nil, fmt.Errorf("discovery is closed")
	}
	
	ch := make(chan []*ServiceInstance, 10)
	
	if _, exists := d.watchers[serviceName]; !exists {
		d.watchers[serviceName] = []chan []*ServiceInstance{}
	}
	d.watchers[serviceName] = append(d.watchers[serviceName], ch)
	
	// Send initial state
	instances := make([]*ServiceInstance, 0)
	if serviceInstances, exists := d.instances[serviceName]; exists {
		for _, inst := range serviceInstances {
			instances = append(instances, inst)
		}
	}
	
	go func() {
		select {
		case ch <- instances:
		case <-ctx.Done():
			d.removeWatcher(serviceName, ch)
			close(ch)
		}
	}()
	
	// Clean up on context cancellation
	go func() {
		<-ctx.Done()
		d.removeWatcher(serviceName, ch)
		close(ch)
	}()
	
	return ch, nil
}

// notifyWatchers notifies all watchers of a service about changes
func (d *LocalDiscovery) notifyWatchers(serviceName string) {
	if watchers, exists := d.watchers[serviceName]; exists {
		instances := make([]*ServiceInstance, 0)
		if serviceInstances, exists := d.instances[serviceName]; exists {
			for _, inst := range serviceInstances {
				instances = append(instances, inst)
			}
		}
		
		for _, ch := range watchers {
			select {
			case ch <- instances:
			default:
				// Channel full, skip
			}
		}
	}
}

// removeWatcher removes a watcher channel
func (d *LocalDiscovery) removeWatcher(serviceName string, ch chan []*ServiceInstance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if watchers, exists := d.watchers[serviceName]; exists {
		newWatchers := []chan []*ServiceInstance{}
		for _, w := range watchers {
			if w != ch {
				newWatchers = append(newWatchers, w)
			}
		}
		d.watchers[serviceName] = newWatchers
	}
}

// Close closes the service discovery
func (d *LocalDiscovery) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.closed {
		return nil
	}
	
	d.closed = true
	
	// Close all watcher channels
	for _, watchers := range d.watchers {
		for _, ch := range watchers {
			close(ch)
		}
	}
	
	return nil
}

// RandomLoadBalancer implements random load balancing
type RandomLoadBalancer struct {
	discovery ServiceDiscovery
}

// NewRandomLoadBalancer creates a new random load balancer
func NewRandomLoadBalancer(discovery ServiceDiscovery) *RandomLoadBalancer {
	return &RandomLoadBalancer{
		discovery: discovery,
	}
}

// GetInstance returns a random healthy instance
func (lb *RandomLoadBalancer) GetInstance(serviceName string) (*ServiceInstance, error) {
	instances, err := lb.discovery.GetInstances(serviceName)
	if err != nil {
		return nil, err
	}
	
	healthyInstances := make([]*ServiceInstance, 0)
	for _, inst := range instances {
		if inst.Healthy {
			healthyInstances = append(healthyInstances, inst)
		}
	}
	
	if len(healthyInstances) == 0 {
		return nil, fmt.Errorf("no healthy instances available")
	}
	
	return healthyInstances[rand.Intn(len(healthyInstances))], nil
}