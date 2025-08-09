package controller

import (
	"context"
	"fmt"
	"sync"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/registry"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// Controller defines the interface for managing service instances
type Controller interface {
	// StartInstance starts a new instance of a service
	StartInstance(ctx context.Context, service *types.ServiceConfig) (*types.Instance, error)
	
	// StopInstance stops a running instance
	StopInstance(ctx context.Context, instanceID string) error
	
	// RestartInstance restarts an instance
	RestartInstance(ctx context.Context, instanceID string) error
	
	// GetInstance gets information about an instance
	GetInstance(ctx context.Context, instanceID string) (*types.Instance, error)
	
	// ListInstances lists all instances managed by this controller
	ListInstances(ctx context.Context) ([]*types.Instance, error)
	
	// ScaleService scales a service to the target number of instances
	ScaleService(ctx context.Context, serviceName string, targetCount int) error
	
	// Cleanup cleans up any orphaned resources
	Cleanup(ctx context.Context) error
	
	// Close closes the controller
	Close() error
}

// ServiceController manages service instances
type ServiceController struct {
	config        *types.AutoScalerConfig
	registry      registry.Registry
	processRunner ProcessRunner
	portAllocator *PortAllocator
	instances     map[string]*ManagedInstance
	mu            sync.RWMutex
}

// ManagedInstance represents an instance managed by the controller
type ManagedInstance struct {
	Instance  *types.Instance
	Process   Process
	StartTime time.Time
	StopChan  chan struct{}
}

// NewServiceController creates a new service controller
func NewServiceController(config *types.AutoScalerConfig, registry registry.Registry, runner ProcessRunner) *ServiceController {
	return &ServiceController{
		config:        config,
		registry:      registry,
		processRunner: runner,
		portAllocator: NewPortAllocator(8100, 8200), // Port range for instances
		instances:     make(map[string]*ManagedInstance),
	}
}

// StartInstance starts a new instance of a service
func (c *ServiceController) StartInstance(ctx context.Context, service *types.ServiceConfig) (*types.Instance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Allocate a port for the new instance
	port, err := c.portAllocator.Allocate()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate port: %w", err)
	}
	
	// Generate instance ID
	instanceID := fmt.Sprintf("%s-%d-%d", service.Name, port, time.Now().UnixNano())
	
	// Create instance object
	instance := &types.Instance{
		ID:          instanceID,
		ServiceName: service.Name,
		Host:        "localhost",
		Port:        port,
		Status:      types.StatusStarting,
		StartTime:   time.Now(),
		Labels: map[string]string{
			"managed_by": "autoscaler",
			"version":    "1.0",
		},
	}
	
	// Register instance with registry
	if err := c.registry.Register(ctx, instance); err != nil {
		c.portAllocator.Release(port)
		return nil, fmt.Errorf("failed to register instance: %w", err)
	}
	
	// Prepare environment variables
	env := make(map[string]string)
	for k, v := range service.Env {
		env[k] = v
	}
	env["PORT"] = fmt.Sprintf("%d", port)
	env["INSTANCE_ID"] = instanceID
	env["SERVICE_NAME"] = service.Name
	
	// Start the process
	process, err := c.processRunner.Start(ProcessConfig{
		Command:  service.StartCommand,
		WorkDir:  service.WorkDir,
		Env:      env,
		Instance: instance,
	})
	if err != nil {
		c.registry.Deregister(ctx, instanceID)
		c.portAllocator.Release(port)
		return nil, fmt.Errorf("failed to start process: %w", err)
	}
	
	// Update instance with PID
	instance.PID = process.PID()
	
	// Store managed instance
	managed := &ManagedInstance{
		Instance:  instance,
		Process:   process,
		StartTime: time.Now(),
		StopChan:  make(chan struct{}),
	}
	c.instances[instanceID] = managed
	
	// Start monitoring goroutine
	go c.monitorInstance(ctx, managed)
	
	// Update status to running after a brief startup period
	go func() {
		time.Sleep(2 * time.Second)
		c.registry.UpdateStatus(ctx, instanceID, types.StatusRunning)
	}()
	
	return instance, nil
}

// StopInstance stops a running instance
func (c *ServiceController) StopInstance(ctx context.Context, instanceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	managed, exists := c.instances[instanceID]
	if !exists {
		return fmt.Errorf("instance %s not found", instanceID)
	}
	
	// Update status to stopping
	c.registry.UpdateStatus(ctx, instanceID, types.StatusStopping)
	
	// Get service config for stop timeout
	serviceConfig, exists := c.config.Services[managed.Instance.ServiceName]
	if !exists {
		return fmt.Errorf("service config not found for %s", managed.Instance.ServiceName)
	}
	
	// Signal monitoring goroutine to stop
	close(managed.StopChan)
	
	// Stop the process with timeout
	stopCtx, cancel := context.WithTimeout(ctx, serviceConfig.StopTimeout)
	defer cancel()
	
	if err := managed.Process.Stop(stopCtx); err != nil {
		// Force kill if graceful stop fails
		if err := managed.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}
	
	// Clean up resources
	c.portAllocator.Release(managed.Instance.Port)
	delete(c.instances, instanceID)
	
	// Update registry
	c.registry.UpdateStatus(ctx, instanceID, types.StatusStopped)
	
	return nil
}

// RestartInstance restarts an instance
func (c *ServiceController) RestartInstance(ctx context.Context, instanceID string) error {
	c.mu.RLock()
	managed, exists := c.instances[instanceID]
	c.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("instance %s not found", instanceID)
	}
	
	// Get service config
	serviceConfig, exists := c.config.Services[managed.Instance.ServiceName]
	if !exists {
		return fmt.Errorf("service config not found for %s", managed.Instance.ServiceName)
	}
	
	// Stop the instance
	if err := c.StopInstance(ctx, instanceID); err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}
	
	// Brief delay to ensure resources are released
	time.Sleep(100 * time.Millisecond)
	
	// Start a new instance
	_, err := c.StartInstance(ctx, serviceConfig)
	if err != nil {
		return fmt.Errorf("failed to start new instance: %w", err)
	}
	
	return nil
}

// GetInstance gets information about an instance
func (c *ServiceController) GetInstance(ctx context.Context, instanceID string) (*types.Instance, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	managed, exists := c.instances[instanceID]
	if !exists {
		// Try to get from registry
		return c.registry.Get(ctx, instanceID)
	}
	
	return managed.Instance, nil
}

// ListInstances lists all instances managed by this controller
func (c *ServiceController) ListInstances(ctx context.Context) ([]*types.Instance, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	instances := make([]*types.Instance, 0, len(c.instances))
	for _, managed := range c.instances {
		instances = append(instances, managed.Instance)
	}
	
	return instances, nil
}

// ScaleService scales a service to the target number of instances
func (c *ServiceController) ScaleService(ctx context.Context, serviceName string, targetCount int) error {
	serviceConfig, exists := c.config.Services[serviceName]
	if !exists {
		return fmt.Errorf("service %s not configured", serviceName)
	}
	
	// Get current instances from registry
	currentInstances, err := c.registry.GetByService(ctx, serviceName)
	if err != nil {
		return fmt.Errorf("failed to get current instances: %w", err)
	}
	
	// Count only running/starting instances
	currentCount := 0
	var runningInstances []*types.Instance
	for _, inst := range currentInstances {
		if inst.Status == types.StatusRunning || inst.Status == types.StatusStarting {
			currentCount++
			runningInstances = append(runningInstances, inst)
		}
	}
	currentInstances = runningInstances
	
	// Scale out
	if targetCount > currentCount {
		toStart := targetCount - currentCount
		for i := 0; i < toStart; i++ {
			_, err := c.StartInstance(ctx, serviceConfig)
			if err != nil {
				return fmt.Errorf("failed to start instance %d/%d: %w", i+1, toStart, err)
			}
			// Brief delay between starts to avoid resource contention
			if i < toStart-1 {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	
	// Scale in
	if targetCount < currentCount {
		toStop := currentCount - targetCount
		
		// Stop the newest instances first (LIFO)
		for i := 0; i < toStop && i < len(currentInstances); i++ {
			instance := currentInstances[len(currentInstances)-1-i]
			if err := c.StopInstance(ctx, instance.ID); err != nil {
				return fmt.Errorf("failed to stop instance %s: %w", instance.ID, err)
			}
			// Brief delay between stops
			if i < toStop-1 {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	
	return nil
}

// Cleanup cleans up any orphaned resources
func (c *ServiceController) Cleanup(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Check for orphaned processes
	for instanceID, managed := range c.instances {
		if !managed.Process.IsRunning() {
			// Process died, clean up
			c.portAllocator.Release(managed.Instance.Port)
			delete(c.instances, instanceID)
			c.registry.UpdateStatus(ctx, instanceID, types.StatusFailed)
		}
	}
	
	return nil
}

// Close closes the controller
func (c *ServiceController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Stop all managed instances
	for instanceID, managed := range c.instances {
		close(managed.StopChan)
		managed.Process.Kill()
		c.portAllocator.Release(managed.Instance.Port)
		c.registry.UpdateStatus(context.Background(), instanceID, types.StatusStopped)
	}
	
	c.instances = make(map[string]*ManagedInstance)
	
	return nil
}

// monitorInstance monitors the health of an instance
func (c *ServiceController) monitorInstance(ctx context.Context, managed *ManagedInstance) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-managed.StopChan:
			return
		case <-ticker.C:
			// Check if process is still running
			if !managed.Process.IsRunning() {
				c.mu.Lock()
				c.registry.UpdateStatus(context.Background(), managed.Instance.ID, types.StatusFailed)
				c.portAllocator.Release(managed.Instance.Port)
				delete(c.instances, managed.Instance.ID)
				c.mu.Unlock()
				return
			}
		}
	}
}