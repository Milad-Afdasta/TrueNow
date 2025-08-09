package autoscaler

import (
	"context"
	"fmt"
	"sync"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/backpressure"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/controller"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/metrics"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/registry"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/scaler"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// AutoScaler coordinates all auto-scaling operations
type AutoScaler struct {
	config       *types.AutoScalerConfig
	registry     registry.Registry
	collector    metrics.Collector
	engine       scaler.Engine
	controller   controller.Controller
	cascade      *scaler.CascadeManager
	backpressure *backpressure.BackpressureCoordinator
	
	// Control channels
	stopChan   chan struct{}
	stopped    chan struct{}
	
	// State
	running    bool
	mu         sync.RWMutex
}

// NewAutoScaler creates a new auto-scaler instance
func NewAutoScaler(config *types.AutoScalerConfig) (*AutoScaler, error) {
	// Validate config
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	
	// Create registry
	reg := registry.NewInMemoryRegistry(registry.DefaultRegistryConfig())
	
	// Create metrics collector config
	collectorConfig := metrics.DefaultCollectorConfig()
	collectorConfig.Endpoint = config.Prometheus.URL
	if config.Prometheus.Timeout > 0 {
		collectorConfig.Timeout = config.Prometheus.Timeout
	}
	
	// Create metrics collector - use mock collector in dry-run mode
	var collector metrics.Collector
	if config.UseMockProcesses {
		collector = metrics.NewMockCollector(collectorConfig)
	} else {
		collector = metrics.NewPrometheusCollector(collectorConfig)
	}
	
	// Create scaling engine
	engine := scaler.NewScalingEngine(config, reg, collector)
	
	// Create process runner
	var processRunner controller.ProcessRunner
	if config.UseMockProcesses {
		processRunner = controller.NewMockProcessRunner()
	} else {
		processRunner = controller.NewLocalProcessRunner()
	}
	
	// Create service controller
	serviceController := controller.NewServiceController(config, reg, processRunner)
	
	// Create cascade manager
	cascadeManager := scaler.NewCascadeManager(engine)
	
	// Add default cascade rules if enabled
	if config.EnableCascading {
		// Gateway -> Stream Ingester (1:0.5 ratio, gateway scales trigger ingester)
		cascadeManager.AddRule(scaler.CascadeRule{
			TriggerService:   "gateway",
			TriggerThreshold: 3,
			TriggerCondition: ">=",
			TargetService:    "stream-ingester",
			ScaleRatio:       0.5,
			MinTarget:        1,
			MaxTarget:        4,
			Delay:            10 * time.Second,
			Priority:         10,
		})
		
		// Stream Ingester -> Hot Tier (1:0.3 ratio)
		cascadeManager.AddRule(scaler.CascadeRule{
			TriggerService:   "stream-ingester",
			TriggerThreshold: 2,
			TriggerCondition: ">=",
			TargetService:    "hot-tier",
			ScaleRatio:       0.3,
			MinTarget:        1,
			MaxTarget:        3,
			Delay:            15 * time.Second,
			Priority:         5,
		})
	}
	
	// Create backpressure coordinator
	backpressureCoordinator := backpressure.NewBackpressureCoordinator(
		backpressure.DefaultBackpressureConfig(),
	)
	
	// Set up backpressure level change callback
	backpressureCoordinator.SetLevelChangeCallback(func(old, new backpressure.BackpressureLevel) {
		fmt.Printf("⚠️  Backpressure level changed: %s → %s\n", old, new)
	})
	
	return &AutoScaler{
		config:       config,
		registry:     reg,
		collector:    collector,
		engine:       engine,
		controller:   serviceController,
		cascade:      cascadeManager,
		backpressure: backpressureCoordinator,
		stopChan:     make(chan struct{}),
		stopped:      make(chan struct{}),
	}, nil
}

// Start begins the auto-scaling loop
func (a *AutoScaler) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("autoscaler already running")
	}
	a.running = true
	a.mu.Unlock()
	
	// Start initial instances for each service
	if err := a.startInitialInstances(ctx); err != nil {
		return fmt.Errorf("failed to start initial instances: %w", err)
	}
	
	// Start the scaling loop
	go a.scalingLoop(ctx)
	
	// Start the cleanup loop
	go a.cleanupLoop(ctx)
	
	// Start backpressure coordinator if enabled
	if a.config.EnableBackpressure && a.backpressure != nil {
		if err := a.backpressure.Start(ctx); err != nil {
			return fmt.Errorf("failed to start backpressure coordinator: %w", err)
		}
	}
	
	return nil
}

// Stop stops the auto-scaler
func (a *AutoScaler) Stop(ctx context.Context) error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	a.mu.Unlock()
	
	// Signal stop
	close(a.stopChan)
	
	// Wait for loops to stop with timeout
	select {
	case <-a.stopped:
		// Stopped gracefully
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for autoscaler to stop")
	}
	
	// Stop all instances
	if err := a.stopAllInstances(ctx); err != nil {
		return fmt.Errorf("failed to stop instances: %w", err)
	}
	
	// Close components
	a.controller.Close()
	a.registry.Close()
	
	return nil
}

// startInitialInstances starts the minimum number of instances for each service
func (a *AutoScaler) startInitialInstances(ctx context.Context) error {
	for serviceName, serviceConfig := range a.config.Services {
		// Check current instances
		instances, err := a.registry.GetByService(ctx, serviceName)
		if err != nil {
			return fmt.Errorf("failed to get instances for %s: %w", serviceName, err)
		}
		
		// Count running instances
		runningCount := 0
		for _, inst := range instances {
			if inst.Status == types.StatusRunning || inst.Status == types.StatusStarting {
				runningCount++
			}
		}
		
		// Start minimum instances if needed
		if runningCount < serviceConfig.MinInstances {
			toStart := serviceConfig.MinInstances - runningCount
			for i := 0; i < toStart; i++ {
				_, err := a.controller.StartInstance(ctx, serviceConfig)
				if err != nil {
					return fmt.Errorf("failed to start instance for %s: %w", serviceName, err)
				}
			}
		}
	}
	
	return nil
}

// stopAllInstances stops all managed instances
func (a *AutoScaler) stopAllInstances(ctx context.Context) error {
	instances, err := a.controller.ListInstances(ctx)
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}
	
	for _, instance := range instances {
		if err := a.controller.StopInstance(ctx, instance.ID); err != nil {
			// Log error but continue
			fmt.Printf("Error stopping instance %s: %v\n", instance.ID, err)
		}
	}
	
	return nil
}

// scalingLoop runs the main scaling evaluation loop
func (a *AutoScaler) scalingLoop(ctx context.Context) {
	defer close(a.stopped)
	
	ticker := time.NewTicker(a.config.EvaluationInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopChan:
			return
		case <-ticker.C:
			a.evaluateScaling(ctx)
		}
	}
}

// cleanupLoop runs periodic cleanup tasks
func (a *AutoScaler) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopChan:
			return
		case <-ticker.C:
			a.controller.Cleanup(ctx)
		}
	}
}

// evaluateScaling evaluates and applies scaling decisions for all services
func (a *AutoScaler) evaluateScaling(ctx context.Context) {
	a.mu.RLock()
	if !a.running {
		a.mu.RUnlock()
		return
	}
	a.mu.RUnlock()
	
	// Evaluate all services
	decisions, err := a.engine.EvaluateAll(ctx)
	if err != nil {
		fmt.Printf("Error evaluating scaling: %v\n", err)
		return
	}
	
	// Get current service states for cascade evaluation
	serviceStates := make(map[string]*types.ServiceState)
	for serviceName, serviceConfig := range a.config.Services {
		instances, err := a.registry.GetByService(ctx, serviceName)
		if err != nil {
			continue
		}
		
		currentCount := 0
		for _, inst := range instances {
			if inst.Status == types.StatusRunning || inst.Status == types.StatusStarting {
				currentCount++
			}
		}
		
		serviceStates[serviceName] = &types.ServiceState{
			ServiceName:      serviceName,
			CurrentInstances: currentCount,
		}
		
		// Update backpressure state if enabled
		if a.config.EnableBackpressure && a.backpressure != nil {
			a.backpressure.UpdateServiceState(&backpressure.ServiceBackpressureState{
				ServiceName:      serviceName,
				CurrentInstances: currentCount,
				MaxInstances:     serviceConfig.MaxInstances,
				IsAtMaxScale:     currentCount >= serviceConfig.MaxInstances,
			})
		}
	}
	
	// Evaluate cascade rules
	if a.config.EnableCascading && a.cascade != nil {
		cascadeDecisions, err := a.cascade.EvaluateCascades(ctx, serviceStates)
		if err != nil {
			fmt.Printf("Error evaluating cascades: %v\n", err)
		} else {
			// Append cascade decisions
			decisions = append(decisions, cascadeDecisions...)
		}
	}
	
	// Apply decisions
	for _, decision := range decisions {
		if decision.Action == types.ActionNone {
			continue
		}
		
		// Get current instance count
		instances, err := a.registry.GetByService(ctx, decision.ServiceName)
		if err != nil {
			fmt.Printf("Error getting instances for %s: %v\n", decision.ServiceName, err)
			continue
		}
		
		// Count running instances
		currentCount := 0
		for _, inst := range instances {
			if inst.Status == types.StatusRunning || inst.Status == types.StatusStarting {
				currentCount++
			}
		}
		
		// Calculate target count
		targetCount := currentCount
		switch decision.Action {
		case types.ActionScaleOut:
			targetCount = decision.TargetCount
		case types.ActionScaleIn:
			targetCount = decision.TargetCount
		}
		
		// Apply scaling if needed
		if targetCount != currentCount {
			fmt.Printf("Scaling %s from %d to %d instances (reason: %s)\n",
				decision.ServiceName, currentCount, targetCount, decision.Reason)
			
			if err := a.controller.ScaleService(ctx, decision.ServiceName, targetCount); err != nil {
				fmt.Printf("Error scaling %s: %v\n", decision.ServiceName, err)
				continue
			}
			
			// Record the decision
			if err := a.engine.ApplyDecision(ctx, decision); err != nil {
				fmt.Printf("Error recording decision for %s: %v\n", decision.ServiceName, err)
			}
		}
	}
}

// GetStatus returns the current status of the autoscaler
func (a *AutoScaler) GetStatus(ctx context.Context) (*Status, error) {
	a.mu.RLock()
	running := a.running
	a.mu.RUnlock()
	
	// Get all instances
	instances, err := a.controller.ListInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}
	
	// Group by service
	serviceStatus := make(map[string]*ServiceStatus)
	for _, inst := range instances {
		if _, exists := serviceStatus[inst.ServiceName]; !exists {
			serviceStatus[inst.ServiceName] = &ServiceStatus{
				ServiceName: inst.ServiceName,
				Instances:   []*types.Instance{},
			}
		}
		serviceStatus[inst.ServiceName].Instances = append(
			serviceStatus[inst.ServiceName].Instances, inst)
	}
	
	// Calculate totals for each service
	for serviceName, status := range serviceStatus {
		config := a.config.Services[serviceName]
		status.MinInstances = config.MinInstances
		status.MaxInstances = config.MaxInstances
		
		for _, inst := range status.Instances {
			if inst.Status == types.StatusRunning {
				status.RunningCount++
			}
		}
		status.TotalCount = len(status.Instances)
	}
	
	return &Status{
		Running:  running,
		Services: serviceStatus,
	}, nil
}

// Status represents the current status of the autoscaler
type Status struct {
	Running  bool                       `json:"running"`
	Services map[string]*ServiceStatus  `json:"services"`
}

// ServiceStatus represents the status of a single service
type ServiceStatus struct {
	ServiceName  string             `json:"service_name"`
	MinInstances int                `json:"min_instances"`
	MaxInstances int                `json:"max_instances"`
	RunningCount int                `json:"running_count"`
	TotalCount   int                `json:"total_count"`
	Instances    []*types.Instance  `json:"instances"`
}

// IsAtMaxScale checks if any service is at max scale
func (a *AutoScaler) IsAtMaxScale(ctx context.Context) (bool, []string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	
	var maxedServices []string
	
	for serviceName, config := range a.config.Services {
		instances, err := a.registry.GetByService(ctx, serviceName)
		if err != nil {
			return false, nil, fmt.Errorf("failed to get instances for %s: %w", serviceName, err)
		}
		
		// Count running instances
		runningCount := 0
		for _, inst := range instances {
			if inst.Status == types.StatusRunning || inst.Status == types.StatusStarting {
				runningCount++
			}
		}
		
		if runningCount >= config.MaxInstances {
			maxedServices = append(maxedServices, serviceName)
		}
	}
	
	return len(maxedServices) > 0, maxedServices, nil
}

// GetBackpressureStatus returns the current backpressure status
func (a *AutoScaler) GetBackpressureStatus() *BackpressureStatus {
	if a.backpressure == nil {
		return nil
	}
	
	metrics := a.backpressure.GetMetrics()
	strategy := a.backpressure.GetResponseStrategy()
	states := a.backpressure.GetServiceStates()
	
	return &BackpressureStatus{
		Enabled:         a.config.EnableBackpressure,
		CurrentLevel:    metrics.CurrentLevel.String(),
		LevelChanges:    metrics.LevelChanges,
		RequestsDropped: metrics.RequestsDropped,
		Strategy:        strategy,
		ServiceStates:   states,
	}
}

// BackpressureStatus represents the current backpressure status
type BackpressureStatus struct {
	Enabled         bool                                             `json:"enabled"`
	CurrentLevel    string                                           `json:"current_level"`
	LevelChanges    uint64                                           `json:"level_changes"`
	RequestsDropped uint64                                           `json:"requests_dropped"`
	Strategy        backpressure.ResponseStrategy                   `json:"strategy"`
	ServiceStates   map[string]*backpressure.ServiceBackpressureState `json:"service_states"`
}