package scaler

import (
	"context"
	"fmt"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/metrics"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/registry"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// Engine defines the interface for the scaling decision engine
type Engine interface {
	// EvaluateService evaluates a service and returns a scaling decision
	EvaluateService(ctx context.Context, serviceName string) (*types.ScalingDecision, error)
	
	// EvaluateAll evaluates all services and returns scaling decisions
	EvaluateAll(ctx context.Context) ([]*types.ScalingDecision, error)
	
	// CanScaleOut checks if a service can scale out
	CanScaleOut(ctx context.Context, serviceName string) (bool, error)
	
	// CanScaleIn checks if a service can scale in
	CanScaleIn(ctx context.Context, serviceName string) (bool, error)
	
	// ApplyDecision applies a scaling decision
	ApplyDecision(ctx context.Context, decision *types.ScalingDecision) error
	
	// GetCooldownStatus gets the cooldown status for a service
	GetCooldownStatus(serviceName string) (bool, time.Time)
	
	// Start starts the scaling engine
	Start(ctx context.Context) error
	
	// Stop stops the scaling engine
	Stop() error
}

// ScalingEngine implements the Engine interface
type ScalingEngine struct {
	config    *types.AutoScalerConfig
	registry  registry.Registry
	collector metrics.Collector
	cooldowns map[string]cooldownInfo
	running   bool
	stopCh    chan struct{}
}

type cooldownInfo struct {
	lastScaleTime time.Time
	lastAction    types.ScalingAction
	cooldownUntil time.Time
}

// NewScalingEngine creates a new scaling engine
func NewScalingEngine(config *types.AutoScalerConfig, registry registry.Registry, collector metrics.Collector) *ScalingEngine {
	return &ScalingEngine{
		config:    config,
		registry:  registry,
		collector: collector,
		cooldowns: make(map[string]cooldownInfo),
		stopCh:    make(chan struct{}),
	}
}

// EvaluateService evaluates a service and returns a scaling decision
func (e *ScalingEngine) EvaluateService(ctx context.Context, serviceName string) (*types.ScalingDecision, error) {
	// Get service configuration
	serviceConfig, exists := e.config.Services[serviceName]
	if !exists {
		return nil, fmt.Errorf("service %s not found in configuration", serviceName)
	}
	
	// Get service state from registry
	state, err := e.registry.GetServiceState(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get service state: %w", err)
	}
	
	// Check if in cooldown
	if inCooldown, until := e.GetCooldownStatus(serviceName); inCooldown {
		return &types.ScalingDecision{
			ServiceName:  serviceName,
			Action:       types.ActionNone,
			CurrentCount: state.CurrentInstances,
			TargetCount:  state.CurrentInstances,
			Reason:       fmt.Sprintf("in cooldown until %s", until.Format("15:04:05")),
			Timestamp:    time.Now(),
		}, nil
	}
	
	// Get healthy instances for accurate scaling decisions
	healthyInstances, err := e.registry.GetHealthy(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get healthy instances: %w", err)
	}
	
	// Collect metrics for the service
	serviceMetrics, err := e.collector.CollectServiceMetrics(ctx, serviceName, healthyInstances)
	if err != nil {
		return nil, fmt.Errorf("failed to collect metrics: %w", err)
	}
	
	// Evaluate metrics against thresholds
	scaleOut := false
	scaleIn := true
	var triggeringMetric string
	var triggeringValue float64
	
	for _, metricConfig := range serviceConfig.Metrics {
		metricValue, exists := serviceMetrics.Metrics[string(metricConfig.Type)]
		if !exists {
			continue
		}
		
		// Check if metric exceeds threshold (scale out)
		if metricValue.Value > metricConfig.Threshold {
			scaleOut = true
			scaleIn = false
			triggeringMetric = string(metricConfig.Type)
			triggeringValue = metricValue.Value
			break // One metric exceeding is enough to scale out
		}
		
		// Check if all metrics are below scale-in threshold (80% of threshold)
		scaleInThreshold := metricConfig.Threshold * 0.8
		if metricValue.Value > scaleInThreshold {
			scaleIn = false
		}
	}
	
	// Make scaling decision
	decision := &types.ScalingDecision{
		ServiceName:  serviceName,
		CurrentCount: state.CurrentInstances,
		Metrics:      make(map[string]float64),
		Timestamp:    time.Now(),
	}
	
	// Copy metrics to decision
	for name, metric := range serviceMetrics.Metrics {
		decision.Metrics[name] = metric.Value
	}
	
	if scaleOut && state.CurrentInstances < serviceConfig.MaxInstances {
		// Scale out
		decision.Action = types.ActionScaleOut
		decision.TargetCount = min(
			state.CurrentInstances + serviceConfig.ScaleOut.Increment,
			serviceConfig.MaxInstances,
		)
		decision.Reason = fmt.Sprintf("%s=%.2f exceeds threshold", triggeringMetric, triggeringValue)
	} else if scaleIn && state.CurrentInstances > serviceConfig.MinInstances && len(healthyInstances) > serviceConfig.MinInstances {
		// Scale in (only if we have enough healthy instances)
		// But don't scale in if we're close to min already (within 20% buffer)
		buffer := float64(serviceConfig.MinInstances) * 1.2
		if float64(state.CurrentInstances) > buffer {
			decision.Action = types.ActionScaleIn
			decision.TargetCount = max(
				state.CurrentInstances - serviceConfig.ScaleIn.Increment,
				serviceConfig.MinInstances,
			)
			decision.Reason = "all metrics below scale-in threshold"
		} else {
			decision.Action = types.ActionNone
			decision.TargetCount = state.CurrentInstances
			decision.Reason = "close to minimum instances"
		}
	} else {
		// No scaling needed
		decision.Action = types.ActionNone
		decision.TargetCount = state.CurrentInstances
		
		if state.CurrentInstances >= serviceConfig.MaxInstances && scaleOut {
			decision.Reason = fmt.Sprintf("at max instances (%d)", serviceConfig.MaxInstances)
		} else if state.CurrentInstances <= serviceConfig.MinInstances && scaleIn {
			decision.Reason = fmt.Sprintf("at min instances (%d)", serviceConfig.MinInstances)
		} else {
			decision.Reason = "metrics within thresholds"
		}
	}
	
	return decision, nil
}

// EvaluateAll evaluates all services and returns scaling decisions
func (e *ScalingEngine) EvaluateAll(ctx context.Context) ([]*types.ScalingDecision, error) {
	decisions := make([]*types.ScalingDecision, 0)
	
	for serviceName := range e.config.Services {
		decision, err := e.EvaluateService(ctx, serviceName)
		if err != nil {
			// Log error but continue with other services
			continue
		}
		decisions = append(decisions, decision)
	}
	
	return decisions, nil
}

// CanScaleOut checks if a service can scale out
func (e *ScalingEngine) CanScaleOut(ctx context.Context, serviceName string) (bool, error) {
	serviceConfig, exists := e.config.Services[serviceName]
	if !exists {
		return false, fmt.Errorf("service %s not found", serviceName)
	}
	
	state, err := e.registry.GetServiceState(ctx, serviceName)
	if err != nil {
		return false, err
	}
	
	// Check max instances limit
	if state.CurrentInstances >= serviceConfig.MaxInstances {
		return false, nil
	}
	
	// Check cooldown
	if inCooldown, _ := e.GetCooldownStatus(serviceName); inCooldown {
		return false, nil
	}
	
	return true, nil
}

// CanScaleIn checks if a service can scale in
func (e *ScalingEngine) CanScaleIn(ctx context.Context, serviceName string) (bool, error) {
	serviceConfig, exists := e.config.Services[serviceName]
	if !exists {
		return false, fmt.Errorf("service %s not found", serviceName)
	}
	
	state, err := e.registry.GetServiceState(ctx, serviceName)
	if err != nil {
		return false, err
	}
	
	// Check min instances limit
	if state.CurrentInstances <= serviceConfig.MinInstances {
		return false, nil
	}
	
	// Check cooldown
	if inCooldown, _ := e.GetCooldownStatus(serviceName); inCooldown {
		return false, nil
	}
	
	// Check if we have enough healthy instances
	healthyInstances, err := e.registry.GetHealthy(ctx, serviceName)
	if err != nil {
		return false, err
	}
	
	if len(healthyInstances) <= serviceConfig.MinInstances {
		return false, nil
	}
	
	return true, nil
}

// ApplyDecision applies a scaling decision
func (e *ScalingEngine) ApplyDecision(ctx context.Context, decision *types.ScalingDecision) error {
	if decision.Action == types.ActionNone {
		return nil
	}
	
	serviceConfig, exists := e.config.Services[decision.ServiceName]
	if !exists {
		return fmt.Errorf("service %s not found", decision.ServiceName)
	}
	
	// Update cooldown
	now := time.Now()
	cooldown := serviceConfig.ScaleOut.Cooldown
	if decision.Action == types.ActionScaleIn {
		cooldown = serviceConfig.ScaleIn.Cooldown
	}
	
	e.cooldowns[decision.ServiceName] = cooldownInfo{
		lastScaleTime: now,
		lastAction:    decision.Action,
		cooldownUntil: now.Add(cooldown),
	}
	
	// Note: Actual scaling (starting/stopping instances) will be handled by the controller
	// This just updates the cooldown state
	
	return nil
}

// GetCooldownStatus gets the cooldown status for a service
func (e *ScalingEngine) GetCooldownStatus(serviceName string) (bool, time.Time) {
	if cooldown, exists := e.cooldowns[serviceName]; exists {
		if time.Now().Before(cooldown.cooldownUntil) {
			return true, cooldown.cooldownUntil
		}
	}
	return false, time.Time{}
}

// Start starts the scaling engine
func (e *ScalingEngine) Start(ctx context.Context) error {
	if e.running {
		return fmt.Errorf("scaling engine already running")
	}
	
	e.running = true
	go e.run(ctx)
	
	return nil
}

// Stop stops the scaling engine
func (e *ScalingEngine) Stop() error {
	if !e.running {
		return nil
	}
	
	e.running = false
	close(e.stopCh)
	
	return nil
}

// run is the main evaluation loop
func (e *ScalingEngine) run(ctx context.Context) {
	ticker := time.NewTicker(e.config.EvaluationInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			e.EvaluateAll(ctx)
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		}
	}
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}