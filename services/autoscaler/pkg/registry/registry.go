package registry

import (
	"context"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// Registry defines the interface for service instance registry
type Registry interface {
	// Register registers a new service instance
	Register(ctx context.Context, instance *types.Instance) error
	
	// Deregister removes a service instance from the registry
	Deregister(ctx context.Context, instanceID string) error
	
	// Update updates an existing instance's information
	Update(ctx context.Context, instance *types.Instance) error
	
	// Get retrieves a specific instance by ID
	Get(ctx context.Context, instanceID string) (*types.Instance, error)
	
	// GetByService retrieves all instances for a specific service
	GetByService(ctx context.Context, serviceName string) ([]*types.Instance, error)
	
	// GetAll retrieves all registered instances
	GetAll(ctx context.Context) ([]*types.Instance, error)
	
	// GetHealthy retrieves all healthy instances for a service
	GetHealthy(ctx context.Context, serviceName string) ([]*types.Instance, error)
	
	// UpdateStatus updates the status of an instance
	UpdateStatus(ctx context.Context, instanceID string, status types.InstanceStatus) error
	
	// UpdateMetrics updates the metrics for an instance
	UpdateMetrics(ctx context.Context, instanceID string, metrics map[string]float64) error
	
	// GetServiceState gets the current state of a service
	GetServiceState(ctx context.Context, serviceName string) (*types.ServiceState, error)
	
	// Watch watches for changes to service instances
	Watch(ctx context.Context, serviceName string) (<-chan RegistryEvent, error)
	
	// Close closes the registry
	Close() error
}

// RegistryEvent represents an event in the registry
type RegistryEvent struct {
	Type       EventType       `json:"type"`
	Instance   *types.Instance `json:"instance"`
	OldStatus  types.InstanceStatus `json:"old_status,omitempty"`
	NewStatus  types.InstanceStatus `json:"new_status,omitempty"`
	Timestamp  time.Time       `json:"timestamp"`
	ServiceName string         `json:"service_name"`
}

// EventType represents the type of registry event
type EventType string

const (
	EventRegistered   EventType = "registered"
	EventDeregistered EventType = "deregistered"
	EventUpdated      EventType = "updated"
	EventStatusChange EventType = "status_change"
	EventHealthCheck  EventType = "health_check"
)

// HealthChecker defines the interface for health checking
type HealthChecker interface {
	// CheckHealth checks the health of an instance
	CheckHealth(ctx context.Context, instance *types.Instance) error
	
	// StartMonitoring starts health monitoring for all instances
	StartMonitoring(ctx context.Context, registry Registry) error
	
	// StopMonitoring stops health monitoring
	StopMonitoring() error
}

// ServiceDiscovery defines the interface for service discovery
type ServiceDiscovery interface {
	// Discover discovers available instances for a service
	Discover(ctx context.Context, serviceName string) ([]*types.Instance, error)
	
	// DiscoverHealthy discovers healthy instances for a service
	DiscoverHealthy(ctx context.Context, serviceName string) ([]*types.Instance, error)
	
	// GetEndpoint gets the best endpoint for a service (load balanced)
	GetEndpoint(ctx context.Context, serviceName string) (string, error)
	
	// GetEndpoints gets all available endpoints for a service
	GetEndpoints(ctx context.Context, serviceName string) ([]string, error)
}

// LoadBalancer defines the interface for load balancing
type LoadBalancer interface {
	// NextInstance gets the next instance to use (round-robin, least-conn, etc)
	NextInstance(instances []*types.Instance) (*types.Instance, error)
	
	// Reset resets the load balancer state
	Reset()
}

// RegistryConfig represents registry configuration
type RegistryConfig struct {
	// TTL for instance registration (requires periodic refresh)
	TTL time.Duration `json:"ttl" yaml:"ttl"`
	
	// HealthCheckInterval is the interval between health checks
	HealthCheckInterval time.Duration `json:"health_check_interval" yaml:"health_check_interval"`
	
	// HealthCheckTimeout is the timeout for health checks
	HealthCheckTimeout time.Duration `json:"health_check_timeout" yaml:"health_check_timeout"`
	
	// MaxRetries for health checks before marking unhealthy
	MaxRetries int `json:"max_retries" yaml:"max_retries"`
	
	// EnableWatching enables event watching
	EnableWatching bool `json:"enable_watching" yaml:"enable_watching"`
	
	// CleanupInterval for removing stale instances
	CleanupInterval time.Duration `json:"cleanup_interval" yaml:"cleanup_interval"`
}

// DefaultRegistryConfig returns default registry configuration
func DefaultRegistryConfig() RegistryConfig {
	return RegistryConfig{
		TTL:                 30 * time.Second,
		HealthCheckInterval: 10 * time.Second,
		HealthCheckTimeout:  5 * time.Second,
		MaxRetries:          3,
		EnableWatching:      true,
		CleanupInterval:     60 * time.Second,
	}
}

// InstanceFilter defines filters for querying instances
type InstanceFilter struct {
	ServiceName string                 `json:"service_name,omitempty"`
	Status      types.InstanceStatus   `json:"status,omitempty"`
	Labels      map[string]string      `json:"labels,omitempty"`
	MinUptime   time.Duration          `json:"min_uptime,omitempty"`
	Host        string                 `json:"host,omitempty"`
}

// Matches checks if an instance matches the filter
func (f *InstanceFilter) Matches(instance *types.Instance) bool {
	if f.ServiceName != "" && instance.ServiceName != f.ServiceName {
		return false
	}
	
	if f.Status != "" && instance.Status != f.Status {
		return false
	}
	
	if f.Host != "" && instance.Host != f.Host {
		return false
	}
	
	if f.MinUptime > 0 && time.Since(instance.StartTime) < f.MinUptime {
		return false
	}
	
	// Check labels
	for key, value := range f.Labels {
		if instanceValue, ok := instance.Labels[key]; !ok || instanceValue != value {
			return false
		}
	}
	
	return true
}