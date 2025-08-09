package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
	
	log "github.com/sirupsen/logrus"
)

// RegistryClient connects to the autoscaler's registry
type RegistryClient struct {
	registryURL string
	httpClient  *http.Client
	discovery   *LocalDiscovery
	
	// Polling
	pollInterval time.Duration
	stopChan     chan struct{}
	wg           sync.WaitGroup
}

// Instance represents an instance from the registry
type Instance struct {
	ID          string    `json:"id"`
	ServiceName string    `json:"service_name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Status      string    `json:"status"`
	StartTime   time.Time `json:"start_time"`
	PID         int       `json:"pid,omitempty"`
}

// NewRegistryClient creates a new registry client
func NewRegistryClient(registryURL string, pollInterval time.Duration) *RegistryClient {
	if registryURL == "" {
		registryURL = "http://localhost:8090" // Default registry endpoint
	}
	
	return &RegistryClient{
		registryURL: registryURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		discovery:    NewLocalDiscovery(),
		pollInterval: pollInterval,
		stopChan:     make(chan struct{}),
	}
}

// Start starts polling the registry
func (rc *RegistryClient) Start(ctx context.Context) error {
	rc.wg.Add(1)
	go rc.pollRegistry(ctx)
	
	log.WithFields(log.Fields{
		"registry_url":  rc.registryURL,
		"poll_interval": rc.pollInterval,
	}).Info("Registry client started")
	
	return nil
}

// Stop stops the registry client
func (rc *RegistryClient) Stop() error {
	close(rc.stopChan)
	rc.wg.Wait()
	return rc.discovery.Close()
}

// GetDiscovery returns the service discovery interface
func (rc *RegistryClient) GetDiscovery() ServiceDiscovery {
	return rc.discovery
}

// pollRegistry polls the registry for service instances
func (rc *RegistryClient) pollRegistry(ctx context.Context) {
	defer rc.wg.Done()
	
	ticker := time.NewTicker(rc.pollInterval)
	defer ticker.Stop()
	
	// Initial poll
	rc.updateInstances(ctx)
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-rc.stopChan:
			return
		case <-ticker.C:
			rc.updateInstances(ctx)
		}
	}
}

// updateInstances fetches instances from the registry and updates local discovery
func (rc *RegistryClient) updateInstances(ctx context.Context) {
	services := []string{"gateway", "stream-ingester", "hot-tier", "query-api"}
	
	for _, service := range services {
		instances, err := rc.fetchServiceInstances(ctx, service)
		if err != nil {
			log.WithError(err).WithField("service", service).Error("Failed to fetch instances")
			continue
		}
		
		// Update local discovery
		rc.syncInstances(service, instances)
	}
}

// fetchServiceInstances fetches instances for a specific service
func (rc *RegistryClient) fetchServiceInstances(ctx context.Context, serviceName string) ([]*Instance, error) {
	url := fmt.Sprintf("%s/api/v1/services/%s/instances", rc.registryURL, serviceName)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch instances: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d", resp.StatusCode)
	}
	
	var instances []*Instance
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	
	return instances, nil
}

// syncInstances synchronizes instances with local discovery
func (rc *RegistryClient) syncInstances(serviceName string, instances []*Instance) {
	// Get current instances from discovery
	currentInstances, _ := rc.discovery.GetInstances(serviceName)
	currentMap := make(map[string]*ServiceInstance)
	for _, inst := range currentInstances {
		currentMap[inst.ID] = inst
	}
	
	// Track seen instances
	seenInstances := make(map[string]bool)
	
	// Update or add instances
	for _, inst := range instances {
		seenInstances[inst.ID] = true
		
		healthy := inst.Status == "running"
		
		if current, exists := currentMap[inst.ID]; exists {
			// Update existing instance
			if current.Healthy != healthy {
				rc.discovery.UpdateHealth(serviceName, inst.ID, healthy)
			}
		} else {
			// Register new instance
			rc.discovery.RegisterInstance(serviceName, &ServiceInstance{
				ID:       inst.ID,
				Host:     inst.Host,
				Port:     inst.Port,
				Healthy:  healthy,
				LastSeen: time.Now(),
			})
		}
	}
	
	// Remove instances that are no longer in the registry
	for id := range currentMap {
		if !seenInstances[id] {
			rc.discovery.DeregisterInstance(serviceName, id)
		}
	}
}

// StaticRegistry provides static service endpoints for fallback
type StaticRegistry struct {
	discovery *LocalDiscovery
}

// NewStaticRegistry creates a new static registry
func NewStaticRegistry() *StaticRegistry {
	discovery := NewLocalDiscovery()
	
	// Register default static instances
	staticInstances := map[string][]*ServiceInstance{
		"stream-ingester": {
			{ID: "stream-ingester-1", Host: "localhost", Port: 8081, Healthy: true},
		},
		"hot-tier": {
			{ID: "hot-tier-1", Host: "localhost", Port: 8082, Healthy: true},
		},
		"query-api": {
			{ID: "query-api-1", Host: "localhost", Port: 8083, Healthy: true},
		},
	}
	
	for service, instances := range staticInstances {
		for _, inst := range instances {
			discovery.RegisterInstance(service, inst)
		}
	}
	
	return &StaticRegistry{
		discovery: discovery,
	}
}

// GetDiscovery returns the service discovery interface
func (sr *StaticRegistry) GetDiscovery() ServiceDiscovery {
	return sr.discovery
}

// DiscoveryMode represents the discovery mode
type DiscoveryMode string

const (
	ModeStatic   DiscoveryMode = "static"
	ModeRegistry DiscoveryMode = "registry"
	ModeHybrid   DiscoveryMode = "hybrid"
)

// DiscoveryFactory creates the appropriate discovery based on mode
func NewDiscoveryFactory(mode DiscoveryMode, registryURL string) (ServiceDiscovery, error) {
	switch mode {
	case ModeStatic:
		return NewStaticRegistry().GetDiscovery(), nil
		
	case ModeRegistry:
		client := NewRegistryClient(registryURL, 10*time.Second)
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start registry client: %w", err)
		}
		return client.GetDiscovery(), nil
		
	case ModeHybrid:
		// Start with static and try to connect to registry
		static := NewStaticRegistry()
		client := NewRegistryClient(registryURL, 10*time.Second)
		
		ctx := context.Background()
		go func() {
			if err := client.Start(ctx); err != nil {
				log.WithError(err).Warn("Failed to start registry client, using static discovery")
			}
		}()
		
		// Return static discovery initially, registry will update it if available
		return static.GetDiscovery(), nil
		
	default:
		return nil, fmt.Errorf("unknown discovery mode: %s", mode)
	}
}