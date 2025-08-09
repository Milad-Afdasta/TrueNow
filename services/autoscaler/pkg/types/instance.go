package types

import (
	"time"
)

// Instance represents a running service instance
type Instance struct {
	ID          string            `json:"id"`
	ServiceName string            `json:"service_name"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Status      InstanceStatus    `json:"status"`
	StartTime   time.Time         `json:"start_time"`
	LastHealthCheck time.Time     `json:"last_health_check,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	PID         int               `json:"pid,omitempty"` // Process ID for local instances
	Labels      map[string]string `json:"labels,omitempty"`
}

// InstanceStatus represents the status of an instance
type InstanceStatus string

const (
	StatusStarting   InstanceStatus = "starting"
	StatusRunning    InstanceStatus = "running"
	StatusUnhealthy  InstanceStatus = "unhealthy"
	StatusStopping   InstanceStatus = "stopping"
	StatusStopped    InstanceStatus = "stopped"
	StatusFailed     InstanceStatus = "failed"
)

// IsHealthy returns true if the instance is in a healthy state
func (i *Instance) IsHealthy() bool {
	return i.Status == StatusRunning
}

// IsTerminal returns true if the instance is in a terminal state
func (i *Instance) IsTerminal() bool {
	return i.Status == StatusStopped || i.Status == StatusFailed
}

// ScalingDecision represents a scaling decision
type ScalingDecision struct {
	ServiceName string         `json:"service_name"`
	Action      ScalingAction  `json:"action"`
	CurrentCount int           `json:"current_count"`
	TargetCount int            `json:"target_count"`
	Reason      string         `json:"reason"`
	Metrics     map[string]float64 `json:"metrics"`
	Timestamp   time.Time      `json:"timestamp"`
}

// ScalingAction represents the type of scaling action
type ScalingAction string

const (
	ActionScaleOut ScalingAction = "scale_out"
	ActionScaleIn  ScalingAction = "scale_in"
	ActionNone     ScalingAction = "none"
)

// ScalingEvent represents a scaling event that occurred
type ScalingEvent struct {
	ID          string        `json:"id"`
	ServiceName string        `json:"service_name"`
	Action      ScalingAction `json:"action"`
	FromCount   int           `json:"from_count"`
	ToCount     int           `json:"to_count"`
	Success     bool          `json:"success"`
	Error       string        `json:"error,omitempty"`
	StartTime   time.Time     `json:"start_time"`
	EndTime     time.Time     `json:"end_time,omitempty"`
	Duration    time.Duration `json:"duration,omitempty"`
}

// ServiceState represents the current state of a service
type ServiceState struct {
	ServiceName      string              `json:"service_name"`
	CurrentInstances int                 `json:"current_instances"`
	HealthyInstances int                 `json:"healthy_instances"`
	TargetInstances  int                 `json:"target_instances"`
	Instances        map[string]*Instance `json:"instances"`
	LastScaleTime    time.Time           `json:"last_scale_time"`
	InCooldown       bool                `json:"in_cooldown"`
	CooldownEndsAt   time.Time           `json:"cooldown_ends_at,omitempty"`
	IsScaling        bool                `json:"is_scaling"`
	CurrentMetrics   map[string]float64  `json:"current_metrics"`
}

// NeedsScaling returns true if the service needs scaling
func (s *ServiceState) NeedsScaling() bool {
	return s.CurrentInstances != s.TargetInstances && !s.IsScaling && !s.InCooldown
}

// IsMaxScale returns true if the service is at maximum scale
func (s *ServiceState) IsMaxScale(config *ServiceConfig) bool {
	return s.CurrentInstances >= config.MaxInstances
}

// IsMinScale returns true if the service is at minimum scale
func (s *ServiceState) IsMinScale(config *ServiceConfig) bool {
	return s.CurrentInstances <= config.MinInstances
}