package scaler

import (
	"context"
	"fmt"
	"sync"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
	log "github.com/sirupsen/logrus"
)

// CascadeRule defines a cascading scaling rule
type CascadeRule struct {
	TriggerService   string        `json:"trigger_service"`
	TriggerThreshold int           `json:"trigger_threshold"` // Number of instances
	TriggerCondition string        `json:"trigger_condition"` // ">=", "<=", "==", ">", "<"
	TargetService    string        `json:"target_service"`
	ScaleRatio       float64       `json:"scale_ratio"`       // How many target instances per trigger instance
	MinTarget        int           `json:"min_target"`        // Minimum target instances
	MaxTarget        int           `json:"max_target"`        // Maximum target instances
	Delay            time.Duration `json:"delay"`             // Delay before applying cascade
	Priority         int           `json:"priority"`          // Higher priority rules execute first
}

// CascadeManager manages cascading scaling rules
type CascadeManager struct {
	rules      []CascadeRule
	engine     Engine
	mu         sync.RWMutex
	
	// Track cascade executions to prevent loops
	executions map[string]time.Time
	
	// Cascade event channel
	eventChan  chan CascadeEvent
}

// CascadeEvent represents a cascade scaling event
type CascadeEvent struct {
	TriggerService string
	TargetService  string
	TriggerCount   int
	TargetCount    int
	Rule          CascadeRule
	Timestamp     time.Time
}

// NewCascadeManager creates a new cascade manager
func NewCascadeManager(engine Engine) *CascadeManager {
	return &CascadeManager{
		rules:      make([]CascadeRule, 0),
		engine:     engine,
		executions: make(map[string]time.Time),
		eventChan:  make(chan CascadeEvent, 100),
	}
}

// AddRule adds a cascading rule
func (cm *CascadeManager) AddRule(rule CascadeRule) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	// Validate rule
	if err := cm.validateRule(rule); err != nil {
		return fmt.Errorf("invalid cascade rule: %w", err)
	}
	
	// Check for circular dependencies
	if cm.hasCircularDependency(rule) {
		return fmt.Errorf("circular dependency detected: %s -> %s", 
			rule.TriggerService, rule.TargetService)
	}
	
	cm.rules = append(cm.rules, rule)
	
	// Sort rules by priority (higher priority first)
	cm.sortRulesByPriority()
	
	log.WithFields(log.Fields{
		"trigger":   rule.TriggerService,
		"target":    rule.TargetService,
		"ratio":     rule.ScaleRatio,
		"priority":  rule.Priority,
	}).Info("Cascade rule added")
	
	return nil
}

// EvaluateCascades evaluates all cascade rules based on current state
func (cm *CascadeManager) EvaluateCascades(ctx context.Context, serviceStates map[string]*types.ServiceState) ([]*types.ScalingDecision, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	decisions := make([]*types.ScalingDecision, 0)
	
	for _, rule := range cm.rules {
		// Check if trigger service exists
		triggerState, exists := serviceStates[rule.TriggerService]
		if !exists {
			continue
		}
		
		// Check if rule should trigger
		if !cm.shouldTrigger(rule, triggerState.CurrentInstances) {
			continue
		}
		
		// Check if we've recently executed this cascade
		if cm.isInCooldown(rule) {
			log.WithFields(log.Fields{
				"trigger": rule.TriggerService,
				"target":  rule.TargetService,
			}).Debug("Cascade rule in cooldown")
			continue
		}
		
		// Calculate target instances
		targetCount := cm.calculateTargetInstances(rule, triggerState.CurrentInstances)
		
		// Get current target service state
		targetState, exists := serviceStates[rule.TargetService]
		if !exists {
			continue
		}
		
		// Create scaling decision if needed
		if targetCount != targetState.CurrentInstances {
			decision := &types.ScalingDecision{
				ServiceName:  rule.TargetService,
				CurrentCount: targetState.CurrentInstances,
				TargetCount:  targetCount,
				Timestamp:    time.Now(),
			}
			
			if targetCount > targetState.CurrentInstances {
				decision.Action = types.ActionScaleOut
				decision.Reason = fmt.Sprintf("Cascade from %s (%d instances)",
					rule.TriggerService, triggerState.CurrentInstances)
			} else {
				decision.Action = types.ActionScaleIn
				decision.Reason = fmt.Sprintf("Cascade scale-in from %s (%d instances)",
					rule.TriggerService, triggerState.CurrentInstances)
			}
			
			decisions = append(decisions, decision)
			
			// Record cascade event
			cm.recordCascadeEvent(CascadeEvent{
				TriggerService: rule.TriggerService,
				TargetService:  rule.TargetService,
				TriggerCount:   triggerState.CurrentInstances,
				TargetCount:    targetCount,
				Rule:          rule,
				Timestamp:     time.Now(),
			})
			
			log.WithFields(log.Fields{
				"trigger":        rule.TriggerService,
				"trigger_count":  triggerState.CurrentInstances,
				"target":         rule.TargetService,
				"current_count":  targetState.CurrentInstances,
				"target_count":   targetCount,
				"action":         decision.Action,
			}).Info("Cascade rule triggered")
		}
	}
	
	return decisions, nil
}

// shouldTrigger checks if a rule should trigger based on condition
func (cm *CascadeManager) shouldTrigger(rule CascadeRule, currentInstances int) bool {
	switch rule.TriggerCondition {
	case ">=":
		return currentInstances >= rule.TriggerThreshold
	case "<=":
		return currentInstances <= rule.TriggerThreshold
	case "==":
		return currentInstances == rule.TriggerThreshold
	case ">":
		return currentInstances > rule.TriggerThreshold
	case "<":
		return currentInstances < rule.TriggerThreshold
	default:
		// Default to >= for backward compatibility
		return currentInstances >= rule.TriggerThreshold
	}
}

// calculateTargetInstances calculates target instances based on rule
func (cm *CascadeManager) calculateTargetInstances(rule CascadeRule, triggerInstances int) int {
	target := int(float64(triggerInstances) * rule.ScaleRatio)
	
	// Apply min/max constraints
	if target < rule.MinTarget {
		target = rule.MinTarget
	}
	if rule.MaxTarget > 0 && target > rule.MaxTarget {
		target = rule.MaxTarget
	}
	
	return target
}

// isInCooldown checks if a cascade rule is in cooldown
func (cm *CascadeManager) isInCooldown(rule CascadeRule) bool {
	key := fmt.Sprintf("%s->%s", rule.TriggerService, rule.TargetService)
	
	if lastExec, exists := cm.executions[key]; exists {
		if time.Since(lastExec) < rule.Delay {
			return true
		}
	}
	
	return false
}

// recordCascadeEvent records a cascade execution
func (cm *CascadeManager) recordCascadeEvent(event CascadeEvent) {
	key := fmt.Sprintf("%s->%s", event.TriggerService, event.TargetService)
	cm.executions[key] = event.Timestamp
	
	// Send to event channel for monitoring
	select {
	case cm.eventChan <- event:
	default:
		// Channel full, drop oldest event
	}
}

// validateRule validates a cascade rule
func (cm *CascadeManager) validateRule(rule CascadeRule) error {
	if rule.TriggerService == "" {
		return fmt.Errorf("trigger service is required")
	}
	
	if rule.TargetService == "" {
		return fmt.Errorf("target service is required")
	}
	
	if rule.TriggerService == rule.TargetService {
		return fmt.Errorf("trigger and target services cannot be the same")
	}
	
	if rule.ScaleRatio <= 0 {
		return fmt.Errorf("scale ratio must be positive")
	}
	
	if rule.MinTarget < 0 {
		return fmt.Errorf("min target cannot be negative")
	}
	
	if rule.MaxTarget > 0 && rule.MaxTarget < rule.MinTarget {
		return fmt.Errorf("max target cannot be less than min target")
	}
	
	return nil
}

// hasCircularDependency checks for circular dependencies
func (cm *CascadeManager) hasCircularDependency(newRule CascadeRule) bool {
	// Build dependency graph
	deps := make(map[string][]string)
	
	for _, rule := range cm.rules {
		deps[rule.TriggerService] = append(deps[rule.TriggerService], rule.TargetService)
	}
	
	// Add new rule to graph
	deps[newRule.TriggerService] = append(deps[newRule.TriggerService], newRule.TargetService)
	
	// Check for cycles using DFS
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	
	var hasCycle func(string) bool
	hasCycle = func(service string) bool {
		visited[service] = true
		recStack[service] = true
		
		for _, target := range deps[service] {
			if !visited[target] {
				if hasCycle(target) {
					return true
				}
			} else if recStack[target] {
				return true
			}
		}
		
		recStack[service] = false
		return false
	}
	
	for service := range deps {
		if !visited[service] {
			if hasCycle(service) {
				return true
			}
		}
	}
	
	return false
}

// sortRulesByPriority sorts rules by priority (higher first)
func (cm *CascadeManager) sortRulesByPriority() {
	// Simple bubble sort for small rule sets
	n := len(cm.rules)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if cm.rules[j].Priority < cm.rules[j+1].Priority {
				cm.rules[j], cm.rules[j+1] = cm.rules[j+1], cm.rules[j]
			}
		}
	}
}

// GetRules returns all cascade rules
func (cm *CascadeManager) GetRules() []CascadeRule {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	rules := make([]CascadeRule, len(cm.rules))
	copy(rules, cm.rules)
	return rules
}

// RemoveRule removes a cascade rule
func (cm *CascadeManager) RemoveRule(triggerService, targetService string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	newRules := make([]CascadeRule, 0, len(cm.rules))
	found := false
	
	for _, rule := range cm.rules {
		if rule.TriggerService == triggerService && rule.TargetService == targetService {
			found = true
			continue
		}
		newRules = append(newRules, rule)
	}
	
	if !found {
		return fmt.Errorf("rule not found: %s -> %s", triggerService, targetService)
	}
	
	cm.rules = newRules
	return nil
}

// GetEventChannel returns the cascade event channel for monitoring
func (cm *CascadeManager) GetEventChannel() <-chan CascadeEvent {
	return cm.eventChan
}

// ClearCooldowns clears all cooldown timers
func (cm *CascadeManager) ClearCooldowns() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.executions = make(map[string]time.Time)
}

// GetCascadeChain returns the chain of cascades that would be triggered
func (cm *CascadeManager) GetCascadeChain(service string, instances int) []CascadeRule {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	chain := make([]CascadeRule, 0)
	visited := make(map[string]bool)
	
	var findChain func(string, int)
	findChain = func(svc string, count int) {
		if visited[svc] {
			return
		}
		visited[svc] = true
		
		for _, rule := range cm.rules {
			if rule.TriggerService == svc && cm.shouldTrigger(rule, count) {
				chain = append(chain, rule)
				targetCount := cm.calculateTargetInstances(rule, count)
				findChain(rule.TargetService, targetCount)
			}
		}
	}
	
	findChain(service, instances)
	return chain
}