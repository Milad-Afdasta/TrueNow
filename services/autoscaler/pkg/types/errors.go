package types

import (
	"fmt"
)

// ConfigError represents a configuration error
type ConfigError struct {
	Message string
}

func (e ConfigError) Error() string {
	return e.Message
}

// ErrInvalidConfig creates a new configuration error
func ErrInvalidConfig(format string, args ...interface{}) error {
	return ConfigError{
		Message: fmt.Sprintf(format, args...),
	}
}

// Common errors
var (
	ErrServiceNotFound = fmt.Errorf("service not found")
	ErrInstanceNotFound = fmt.Errorf("instance not found")
	ErrScalingInProgress = fmt.Errorf("scaling operation already in progress")
	ErrCooldownActive = fmt.Errorf("cooldown period is active")
	ErrMaxInstancesReached = fmt.Errorf("maximum instances reached")
	ErrMinInstancesReached = fmt.Errorf("minimum instances reached")
	ErrHealthCheckFailed = fmt.Errorf("health check failed")
	ErrMetricsUnavailable = fmt.Errorf("metrics unavailable")
)