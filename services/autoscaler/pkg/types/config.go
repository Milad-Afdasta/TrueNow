package types

import (
	"time"
)

// ServiceConfig defines the scaling configuration for a service
type ServiceConfig struct {
	Name         string            `json:"name" yaml:"name"`
	MinInstances int               `json:"min_instances" yaml:"min_instances"`
	MaxInstances int               `json:"max_instances" yaml:"max_instances"`
	CurrentInstances int           `json:"current_instances,omitempty" yaml:"current_instances,omitempty"`
	Metrics      []MetricConfig    `json:"metrics" yaml:"metrics"`
	ScaleOut     ScalingPolicy     `json:"scale_out" yaml:"scale_out"`
	ScaleIn      ScalingPolicy     `json:"scale_in" yaml:"scale_in"`
	Stateful     bool              `json:"stateful,omitempty" yaml:"stateful,omitempty"`
	HealthCheck  HealthCheckConfig `json:"health_check" yaml:"health_check"`
	StartCommand string            `json:"start_command" yaml:"start_command"`
	StopTimeout  time.Duration     `json:"stop_timeout,omitempty" yaml:"stop_timeout,omitempty"`
	WorkDir      string            `json:"work_dir" yaml:"work_dir"`
	Env          map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

// MetricConfig defines a metric used for scaling decisions
type MetricConfig struct {
	Type        MetricType    `json:"type" yaml:"type"`
	Threshold   float64       `json:"threshold" yaml:"threshold"`
	Aggregation Aggregation   `json:"aggregation" yaml:"aggregation"`
	Window      time.Duration `json:"window" yaml:"window"`
	Query       string        `json:"query,omitempty" yaml:"query,omitempty"` // For custom Prometheus queries
}

// ScalingPolicy defines how to scale in or out
type ScalingPolicy struct {
	Increment int           `json:"increment" yaml:"increment"` // Number of instances to add/remove
	Cooldown  time.Duration `json:"cooldown" yaml:"cooldown"`   // Time to wait before next scaling action
	Strategy  ScaleStrategy `json:"strategy,omitempty" yaml:"strategy,omitempty"`
}

// HealthCheckConfig defines health check settings
type HealthCheckConfig struct {
	Endpoint string        `json:"endpoint" yaml:"endpoint"`
	Interval time.Duration `json:"interval" yaml:"interval"`
	Timeout  time.Duration `json:"timeout" yaml:"timeout"`
	Retries  int           `json:"retries" yaml:"retries"`
}

// MetricType defines the type of metric
type MetricType string

const (
	MetricCPU          MetricType = "cpu"
	MetricMemory       MetricType = "memory"
	MetricRequestRate  MetricType = "request_rate"
	MetricResponseTime MetricType = "response_time"
	MetricQueueDepth   MetricType = "queue_depth"
	MetricKafkaLag     MetricType = "kafka_lag"
	MetricCustom       MetricType = "custom"
)

// Aggregation defines how to aggregate metrics
type Aggregation string

const (
	AggregationAvg Aggregation = "avg"
	AggregationMax Aggregation = "max"
	AggregationMin Aggregation = "min"
	AggregationSum Aggregation = "sum"
	AggregationP95 Aggregation = "p95"
	AggregationP99 Aggregation = "p99"
)

// ScaleStrategy defines the scaling strategy
type ScaleStrategy string

const (
	StrategyIncremental ScaleStrategy = "incremental" // Add/remove fixed number
	StrategyProportional ScaleStrategy = "proportional" // Scale by percentage
	StrategyPredictive ScaleStrategy = "predictive" // Use prediction model
)

// AutoScalerConfig is the root configuration
type AutoScalerConfig struct {
	Services           map[string]*ServiceConfig `json:"services" yaml:"services"`
	DefaultCooldown    time.Duration            `json:"default_cooldown,omitempty" yaml:"default_cooldown,omitempty"`
	MetricsEndpoint    string                   `json:"metrics_endpoint" yaml:"metrics_endpoint"`
	RegistryEndpoint   string                   `json:"registry_endpoint,omitempty" yaml:"registry_endpoint,omitempty"`
	EvaluationInterval time.Duration            `json:"evaluation_interval" yaml:"evaluation_interval"`
	MaxScaleRate       int                      `json:"max_scale_rate,omitempty" yaml:"max_scale_rate,omitempty"` // Max instances to scale per minute
	EnableCascading    bool                     `json:"enable_cascading,omitempty" yaml:"enable_cascading,omitempty"`
	EnableBackpressure bool                     `json:"enable_backpressure,omitempty" yaml:"enable_backpressure,omitempty"`
	LogLevel           string                   `json:"log_level,omitempty" yaml:"log_level,omitempty"`
	UseMockProcesses   bool                     `json:"use_mock_processes,omitempty" yaml:"use_mock_processes,omitempty"`
	MetricsWindow      time.Duration            `json:"metrics_window,omitempty" yaml:"metrics_window,omitempty"`
	Prometheus         PrometheusConfig         `json:"prometheus,omitempty" yaml:"prometheus,omitempty"`
}

// PrometheusConfig defines Prometheus connection settings
type PrometheusConfig struct {
	URL              string        `json:"url" yaml:"url"`
	Timeout          time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Username         string        `json:"username,omitempty" yaml:"username,omitempty"`
	Password         string        `json:"password,omitempty" yaml:"password,omitempty"`
	InsecureSkipVerify bool       `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
}

// CascadeRule defines cascading scaling rules
type CascadeRule struct {
	TriggerService   string        `json:"trigger_service" yaml:"trigger_service"`
	TriggerThreshold int           `json:"trigger_threshold" yaml:"trigger_threshold"` // Number of instances
	DependentService string        `json:"dependent_service" yaml:"dependent_service"`
	ScaleRatio       float64       `json:"scale_ratio" yaml:"scale_ratio"`
	Delay            time.Duration `json:"delay" yaml:"delay"`
}

// Validate checks if the configuration is valid
func (c *ServiceConfig) Validate() error {
	if c.Name == "" {
		return ErrInvalidConfig("service name is required")
	}
	if c.MinInstances < 0 {
		return ErrInvalidConfig("min_instances cannot be negative")
	}
	if c.MaxInstances < c.MinInstances {
		return ErrInvalidConfig("max_instances cannot be less than min_instances")
	}
	if c.MaxInstances == 0 {
		return ErrInvalidConfig("max_instances must be greater than 0")
	}
	if len(c.Metrics) == 0 {
		return ErrInvalidConfig("at least one metric is required")
	}
	if c.ScaleOut.Increment <= 0 {
		return ErrInvalidConfig("scale_out increment must be positive")
	}
	if c.ScaleIn.Increment <= 0 {
		return ErrInvalidConfig("scale_in increment must be positive")
	}
	if c.ScaleOut.Cooldown < time.Second {
		return ErrInvalidConfig("scale_out cooldown must be at least 1 second")
	}
	if c.ScaleIn.Cooldown < time.Second {
		return ErrInvalidConfig("scale_in cooldown must be at least 1 second")
	}
	if c.StartCommand == "" {
		return ErrInvalidConfig("start_command is required")
	}
	return nil
}

// Validate checks if the autoscaler configuration is valid
func (c *AutoScalerConfig) Validate() error {
	if len(c.Services) == 0 {
		return ErrInvalidConfig("at least one service must be configured")
	}
	// Skip prometheus requirement for mock mode
	if !c.UseMockProcesses && c.Prometheus.URL == "" && c.MetricsEndpoint == "" {
		return ErrInvalidConfig("prometheus.url or metrics_endpoint is required")
	}
	if c.EvaluationInterval < time.Second {
		return ErrInvalidConfig("evaluation_interval must be at least 1 second")
	}
	
	// Set defaults
	if c.MetricsWindow == 0 {
		c.MetricsWindow = 5 * time.Minute
	}
	if c.Prometheus.Timeout == 0 {
		c.Prometheus.Timeout = 10 * time.Second
	}
	
	// Validate each service
	for name, svc := range c.Services {
		if svc.Name != name {
			svc.Name = name // Ensure consistency
		}
		if err := svc.Validate(); err != nil {
			return ErrInvalidConfig("service %s: %v", name, err)
		}
		// Set default stop timeout
		if svc.StopTimeout == 0 {
			svc.StopTimeout = 10 * time.Second
		}
	}
	
	return nil
}