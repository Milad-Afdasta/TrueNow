package scaler

import (
	"context"
	"fmt"
	"sync"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/metrics"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// ThresholdEvaluator evaluates metrics against configured thresholds
type ThresholdEvaluator struct {
	config    *types.AutoScalerConfig
	collector metrics.Collector
	history   map[string]*MetricHistory
	mu        sync.RWMutex
}

// MetricHistory tracks historical metric values for a service
type MetricHistory struct {
	ServiceName string
	Entries     []MetricEntry
	MaxEntries  int
}

// MetricEntry represents a point-in-time metric measurement
type MetricEntry struct {
	Timestamp time.Time
	Metrics   map[string]float64
}

// NewThresholdEvaluator creates a new threshold evaluator
func NewThresholdEvaluator(config *types.AutoScalerConfig, collector metrics.Collector) *ThresholdEvaluator {
	return &ThresholdEvaluator{
		config:    config,
		collector: collector,
		history:   make(map[string]*MetricHistory),
	}
}

// EvaluateMetrics evaluates current metrics against thresholds
func (t *ThresholdEvaluator) EvaluateMetrics(ctx context.Context, serviceName string, instances []*types.Instance) (*MetricEvaluation, error) {
	serviceConfig, exists := t.config.Services[serviceName]
	if !exists {
		return nil, fmt.Errorf("service %s not configured", serviceName)
	}
	
	// Collect current metrics
	serviceMetrics, err := t.collector.CollectServiceMetrics(ctx, serviceName, instances)
	if err != nil {
		return nil, fmt.Errorf("failed to collect metrics: %w", err)
	}
	
	// Store in history
	t.addToHistory(serviceName, serviceMetrics.Metrics)
	
	// Evaluate each configured metric
	evaluation := &MetricEvaluation{
		ServiceName:      serviceName,
		Timestamp:        time.Now(),
		MetricResults:    make([]MetricResult, 0),
		ShouldScaleOut:   false,
		ShouldScaleIn:    true,
		CurrentInstances: len(instances),
	}
	
	for _, metricConfig := range serviceConfig.Metrics {
		result := t.evaluateMetric(serviceMetrics.Metrics, metricConfig)
		evaluation.MetricResults = append(evaluation.MetricResults, result)
		
		// Update scaling recommendations
		if result.ExceedsThreshold {
			evaluation.ShouldScaleOut = true
			evaluation.ShouldScaleIn = false
			evaluation.TriggeringMetric = string(metricConfig.Type)
			evaluation.TriggeringValue = result.CurrentValue
		} else if result.CurrentValue > metricConfig.Threshold*0.8 {
			// If any metric is above 80% of threshold, don't scale in
			evaluation.ShouldScaleIn = false
		}
	}
	
	// Check if we have sustained load (for scale out)
	if evaluation.ShouldScaleOut {
		evaluation.SustainedLoad = t.checkSustainedLoad(serviceName, evaluation.TriggeringMetric, serviceConfig)
	}
	
	// Check if we have sustained low load (for scale in)
	if evaluation.ShouldScaleIn {
		evaluation.SustainedLowLoad = t.checkSustainedLowLoad(serviceName, serviceConfig)
	}
	
	return evaluation, nil
}

// evaluateMetric evaluates a single metric against its threshold
func (t *ThresholdEvaluator) evaluateMetric(metrics map[string]metrics.MetricValue, config types.MetricConfig) MetricResult {
	result := MetricResult{
		MetricType:       config.Type,
		Threshold:        config.Threshold,
		Window:           config.Window,
		Aggregation:      config.Aggregation,
		ExceedsThreshold: false,
	}
	
	// Get the metric value
	if metricValue, exists := metrics[string(config.Type)]; exists {
		result.CurrentValue = metricValue.Value
		result.ExceedsThreshold = metricValue.Value > config.Threshold
		
		// Calculate percentage of threshold
		if config.Threshold > 0 {
			result.PercentOfThreshold = (metricValue.Value / config.Threshold) * 100
		}
	}
	
	return result
}

// checkSustainedLoad checks if high load has been sustained over time
func (t *ThresholdEvaluator) checkSustainedLoad(serviceName, metricName string, config *types.ServiceConfig) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	history, exists := t.history[serviceName]
	if !exists || len(history.Entries) < 3 {
		return false // Need at least 3 data points
	}
	
	// Find the metric config
	var metricConfig *types.MetricConfig
	for i := range config.Metrics {
		if string(config.Metrics[i].Type) == metricName {
			metricConfig = &config.Metrics[i]
			break
		}
	}
	
	if metricConfig == nil {
		return false
	}
	
	// Check last N entries
	sustainedCount := 0
	checkCount := min(5, len(history.Entries))
	
	for i := len(history.Entries) - checkCount; i < len(history.Entries); i++ {
		if value, exists := history.Entries[i].Metrics[metricName]; exists {
			if value > metricConfig.Threshold {
				sustainedCount++
			}
		}
	}
	
	// Require at least 60% of recent measurements to exceed threshold
	return float64(sustainedCount)/float64(checkCount) >= 0.6
}

// checkSustainedLowLoad checks if low load has been sustained over time
func (t *ThresholdEvaluator) checkSustainedLowLoad(serviceName string, config *types.ServiceConfig) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	history, exists := t.history[serviceName]
	if !exists || len(history.Entries) < 5 {
		return false // Need at least 5 data points for scale in
	}
	
	// Check last N entries
	checkCount := min(10, len(history.Entries))
	
	for i := len(history.Entries) - checkCount; i < len(history.Entries); i++ {
		entry := history.Entries[i]
		
		// Check if any metric exceeds 80% of its threshold
		for _, metricConfig := range config.Metrics {
			if value, exists := entry.Metrics[string(metricConfig.Type)]; exists {
				if value > metricConfig.Threshold*0.8 {
					return false // Load not consistently low
				}
			}
		}
	}
	
	return true
}

// addToHistory adds a metric entry to the history
func (t *ThresholdEvaluator) addToHistory(serviceName string, metrics map[string]metrics.MetricValue) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if _, exists := t.history[serviceName]; !exists {
		t.history[serviceName] = &MetricHistory{
			ServiceName: serviceName,
			Entries:     make([]MetricEntry, 0),
			MaxEntries:  100,
		}
	}
	
	history := t.history[serviceName]
	
	// Convert metrics to simple map
	simpleMetrics := make(map[string]float64)
	for name, value := range metrics {
		simpleMetrics[name] = value.Value
	}
	
	// Add new entry
	history.Entries = append(history.Entries, MetricEntry{
		Timestamp: time.Now(),
		Metrics:   simpleMetrics,
	})
	
	// Trim if necessary
	if len(history.Entries) > history.MaxEntries {
		history.Entries = history.Entries[len(history.Entries)-history.MaxEntries:]
	}
}

// GetHistory gets the metric history for a service
func (t *ThresholdEvaluator) GetHistory(serviceName string) (*MetricHistory, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	history, exists := t.history[serviceName]
	return history, exists
}

// ClearHistory clears the metric history for a service
func (t *ThresholdEvaluator) ClearHistory(serviceName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	delete(t.history, serviceName)
}

// MetricEvaluation represents the result of metric evaluation
type MetricEvaluation struct {
	ServiceName      string         `json:"service_name"`
	Timestamp        time.Time      `json:"timestamp"`
	MetricResults    []MetricResult `json:"metric_results"`
	ShouldScaleOut   bool           `json:"should_scale_out"`
	ShouldScaleIn    bool           `json:"should_scale_in"`
	SustainedLoad    bool           `json:"sustained_load"`
	SustainedLowLoad bool           `json:"sustained_low_load"`
	TriggeringMetric string         `json:"triggering_metric,omitempty"`
	TriggeringValue  float64        `json:"triggering_value,omitempty"`
	CurrentInstances int            `json:"current_instances"`
}

// MetricResult represents the evaluation result for a single metric
type MetricResult struct {
	MetricType         types.MetricType  `json:"metric_type"`
	CurrentValue       float64           `json:"current_value"`
	Threshold          float64           `json:"threshold"`
	ExceedsThreshold   bool              `json:"exceeds_threshold"`
	PercentOfThreshold float64           `json:"percent_of_threshold"`
	Window             time.Duration     `json:"window"`
	Aggregation        types.Aggregation `json:"aggregation"`
}