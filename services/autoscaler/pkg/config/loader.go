package config

import (
	"fmt"
	"io/ioutil"
	"time"
	
	"gopkg.in/yaml.v3"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*types.AutoScalerConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	
	var rawConfig map[string]interface{}
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	
	// Parse durations manually for better error handling
	config := &types.AutoScalerConfig{
		Services: make(map[string]*types.ServiceConfig),
	}
	
	// Parse basic fields
	if v, ok := rawConfig["evaluation_interval"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid evaluation_interval: %w", err)
		}
		config.EvaluationInterval = d
	}
	
	if v, ok := rawConfig["metrics_window"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid metrics_window: %w", err)
		}
		config.MetricsWindow = d
	}
	
	// Parse Prometheus config
	if prom, ok := rawConfig["prometheus"].(map[string]interface{}); ok {
		config.Prometheus.URL = getStringOrDefault(prom, "url", "http://localhost:9090")
		
		if v, ok := prom["timeout"].(string); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("invalid prometheus.timeout: %w", err)
			}
			config.Prometheus.Timeout = d
		}
	}
	
	// Parse services
	if services, ok := rawConfig["services"].(map[string]interface{}); ok {
		for name, svcData := range services {
			svc, err := parseService(name, svcData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse service %s: %w", name, err)
			}
			config.Services[name] = svc
		}
	}
	
	// Set defaults and validate
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	
	return config, nil
}

func parseService(name string, data interface{}) (*types.ServiceConfig, error) {
	svcMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid service configuration")
	}
	
	svc := &types.ServiceConfig{
		Name:         name,
		MinInstances: getIntOrDefault(svcMap, "min_instances", 1),
		MaxInstances: getIntOrDefault(svcMap, "max_instances", 10),
		StartCommand: getStringOrDefault(svcMap, "start_command", ""),
		WorkDir:      getStringOrDefault(svcMap, "work_dir", "."),
		Env:          make(map[string]string),
		Metrics:      []types.MetricConfig{},
	}
	
	// Parse stop timeout
	if v, ok := svcMap["stop_timeout"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid stop_timeout: %w", err)
		}
		svc.StopTimeout = d
	}
	
	// Parse environment variables
	if env, ok := svcMap["env"].(map[string]interface{}); ok {
		for k, v := range env {
			svc.Env[k] = fmt.Sprintf("%v", v)
		}
	}
	
	// Parse metrics
	if metrics, ok := svcMap["metrics"].([]interface{}); ok {
		for _, metricData := range metrics {
			metric, err := parseMetric(metricData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse metric: %w", err)
			}
			svc.Metrics = append(svc.Metrics, *metric)
		}
	}
	
	// Parse scale out policy
	if scaleOut, ok := svcMap["scale_out"].(map[string]interface{}); ok {
		svc.ScaleOut = parseScalingPolicy(scaleOut)
	}
	
	// Parse scale in policy
	if scaleIn, ok := svcMap["scale_in"].(map[string]interface{}); ok {
		svc.ScaleIn = parseScalingPolicy(scaleIn)
	}
	
	// Parse health check
	if hc, ok := svcMap["health_check"].(map[string]interface{}); ok {
		svc.HealthCheck = parseHealthCheck(hc)
	}
	
	return svc, nil
}

func parseMetric(data interface{}) (*types.MetricConfig, error) {
	metricMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid metric configuration")
	}
	
	metric := &types.MetricConfig{
		Type:        types.MetricType(getStringOrDefault(metricMap, "type", "cpu")),
		Threshold:   getFloatOrDefault(metricMap, "threshold", 80),
		Aggregation: types.Aggregation(getStringOrDefault(metricMap, "aggregation", "avg")),
	}
	
	// Parse window
	if v, ok := metricMap["window"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid window: %w", err)
		}
		metric.Window = d
	}
	
	metric.Query = getStringOrDefault(metricMap, "query", "")
	
	return metric, nil
}

func parseScalingPolicy(data map[string]interface{}) types.ScalingPolicy {
	policy := types.ScalingPolicy{
		Increment: getIntOrDefault(data, "increment", 1),
	}
	
	if v, ok := data["cooldown"].(string); ok {
		d, _ := time.ParseDuration(v)
		policy.Cooldown = d
	}
	
	if v, ok := data["strategy"].(string); ok {
		policy.Strategy = types.ScaleStrategy(v)
	}
	
	return policy
}

func parseHealthCheck(data map[string]interface{}) types.HealthCheckConfig {
	hc := types.HealthCheckConfig{
		Endpoint: getStringOrDefault(data, "endpoint", "/health"),
		Retries:  getIntOrDefault(data, "retries", 3),
	}
	
	if v, ok := data["interval"].(string); ok {
		d, _ := time.ParseDuration(v)
		hc.Interval = d
	}
	
	if v, ok := data["timeout"].(string); ok {
		d, _ := time.ParseDuration(v)
		hc.Timeout = d
	}
	
	return hc
}

func getStringOrDefault(m map[string]interface{}, key, defaultValue string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return defaultValue
}

func getIntOrDefault(m map[string]interface{}, key string, defaultValue int) int {
	if v, ok := m[key].(int); ok {
		return v
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return defaultValue
}

func getFloatOrDefault(m map[string]interface{}, key string, defaultValue float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	if v, ok := m[key].(int); ok {
		return float64(v)
	}
	return defaultValue
}