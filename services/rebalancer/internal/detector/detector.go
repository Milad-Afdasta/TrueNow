package detector

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/pkg/types"
)

// HotspotDetector detects load imbalances and hot keys
type HotspotDetector struct {
	mu sync.RWMutex
	
	config           *types.RebalancerConfig
	metricsHistory   map[int32][]*types.ShardMetrics // Rolling window of metrics
	historySize      int
	lastAnalysis     time.Time
	lastDistribution *types.LoadDistribution
	
	// Statistical tracking
	movingAverage    map[int32]*ExponentialMovingAverage
	standardDev      map[int32]float64
	
	// Pattern detection
	patterns         map[int32]*LoadPattern
}

// LoadPattern tracks load patterns over time
type LoadPattern struct {
	TrendDirection   int       // -1: decreasing, 0: stable, 1: increasing
	Volatility       float64   // Standard deviation of changes
	Periodicity      int       // Detected period in samples (0 = no pattern)
	BurstCount       int       // Number of traffic bursts detected
	LastBurst        time.Time
	SustainedHot     bool      // True if consistently hot
	HotDuration      time.Duration
}

// ExponentialMovingAverage tracks EMA for smoothing
type ExponentialMovingAverage struct {
	value float64
	alpha float64 // Smoothing factor (0-1)
	count int
}

// NewHotspotDetector creates a new hotspot detector
func NewHotspotDetector(config *types.RebalancerConfig) *HotspotDetector {
	return &HotspotDetector{
		config:         config,
		metricsHistory: make(map[int32][]*types.ShardMetrics),
		historySize:    60, // Keep 60 samples (5 minutes at 5s intervals)
		movingAverage:  make(map[int32]*ExponentialMovingAverage),
		standardDev:    make(map[int32]float64),
		patterns:       make(map[int32]*LoadPattern),
	}
}

// GetShardMetrics returns the latest metrics for a shard
func (d *HotspotDetector) GetShardMetrics(shardID int32) *types.ShardMetrics {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	history, exists := d.metricsHistory[shardID]
	if !exists || len(history) == 0 {
		return nil
	}
	
	return history[len(history)-1]
}

// UpdateMetrics updates metrics for a shard
func (d *HotspotDetector) UpdateMetrics(shardID int32, metrics *types.ShardMetrics) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	// Add to history
	history := d.metricsHistory[shardID]
	history = append(history, metrics)
	
	// Maintain rolling window
	if len(history) > d.historySize {
		history = history[len(history)-d.historySize:]
	}
	d.metricsHistory[shardID] = history
	
	// Update moving average
	if ema, exists := d.movingAverage[shardID]; exists {
		ema.Update(metrics.RequestsPerSecond)
	} else {
		d.movingAverage[shardID] = &ExponentialMovingAverage{
			value: metrics.RequestsPerSecond,
			alpha: 0.2, // Adjust based on responsiveness needs
			count: 1,
		}
	}
	
	// Update standard deviation
	d.updateStandardDeviation(shardID)
	
	// Detect patterns
	d.detectPatterns(shardID)
}

// AnalyzeDistribution analyzes current load distribution
func (d *HotspotDetector) AnalyzeDistribution() *types.LoadDistribution {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	dist := &types.LoadDistribution{
		ShardMetrics:    make(map[int32]*types.ShardMetrics),
		Recommendations: []types.RebalanceRecommendation{},
		LastAnalysis:    time.Now(),
	}
	
	// Collect latest metrics
	totalRPS := 0.0
	totalBPS := 0.0
	rpsValues := []float64{}
	
	for shardID, history := range d.metricsHistory {
		if len(history) == 0 {
			continue
		}
		
		latest := history[len(history)-1]
		dist.ShardMetrics[shardID] = latest
		
		totalRPS += latest.RequestsPerSecond
		totalBPS += latest.BytesPerSecond
		rpsValues = append(rpsValues, latest.RequestsPerSecond)
	}
	
	dist.TotalRPS = totalRPS
	dist.TotalBPS = totalBPS
	
	if len(rpsValues) == 0 {
		return dist
	}
	
	// Calculate statistics
	avgRPS := totalRPS / float64(len(rpsValues))
	dist.AverageLoad = avgRPS
	
	// Calculate standard deviation
	variance := 0.0
	for _, rps := range rpsValues {
		diff := rps - avgRPS
		variance += diff * diff
	}
	if len(rpsValues) > 0 {
		variance /= float64(len(rpsValues))
		dist.StdDeviation = math.Sqrt(variance)
	}
	
	// Calculate imbalance ratio (max/avg)
	maxRPS := 0.0
	minRPS := math.MaxFloat64
	for _, rps := range rpsValues {
		if rps > maxRPS {
			maxRPS = rps
		}
		if rps < minRPS && rps > 0 {
			minRPS = rps
		}
	}
	
	if avgRPS > 0 {
		dist.ImbalanceRatio = maxRPS / avgRPS
	}
	
	// Calculate Gini coefficient for inequality measurement
	dist.GiniCoefficient = d.calculateGiniCoefficient(rpsValues)
	
	// Identify hot and cold shards
	for shardID, metrics := range dist.ShardMetrics {
		rpsRatio := metrics.RequestsPerSecond / avgRPS
		
		if rpsRatio > d.config.HotShardThreshold {
			dist.HotShards = append(dist.HotShards, shardID)
			
			// Check if it's sustained hot
			if pattern := d.patterns[shardID]; pattern != nil && pattern.SustainedHot {
				// High priority recommendation for sustained hotspots
				dist.Recommendations = append(dist.Recommendations, d.recommendActionForHotShard(shardID, metrics, rpsRatio, true))
			} else {
				// Lower priority for transient hotspots
				dist.Recommendations = append(dist.Recommendations, d.recommendActionForHotShard(shardID, metrics, rpsRatio, false))
			}
		} else if rpsRatio < d.config.ColdShardThreshold {
			dist.ColdShards = append(dist.ColdShards, shardID)
			
			// Consider merging cold shards
			if metrics.KeyCount < d.config.MinShardSize {
				dist.Recommendations = append(dist.Recommendations, types.RebalanceRecommendation{
					Action:          types.ActionTypeMergeShard,
					ShardID:         shardID,
					EstimatedImpact: 0.1, // Merging has lower impact
					Risk:            types.RiskLevelLow,
					Reason:          "Shard underutilized with low key count",
				})
			}
		}
	}
	
	// Check for severe imbalance
	if dist.GiniCoefficient > d.config.ImbalanceThreshold {
		dist.Recommendations = append(dist.Recommendations, types.RebalanceRecommendation{
			Action:          types.ActionTypeRedistribute,
			EstimatedImpact: dist.GiniCoefficient - d.config.ImbalanceThreshold,
			Risk:            types.RiskLevelMedium,
			Reason:          "Severe load imbalance detected across shards",
		})
	}
	
	d.lastDistribution = dist
	d.lastAnalysis = time.Now()
	
	return dist
}

// recommendActionForHotShard generates recommendation for a hot shard
func (d *HotspotDetector) recommendActionForHotShard(shardID int32, metrics *types.ShardMetrics, rpsRatio float64, sustained bool) types.RebalanceRecommendation {
	// Check for hot keys first
	if len(metrics.HotKeys) > 0 {
		// If we have specific hot keys, recommend salting
		totalHotKeyRPS := 0.0
		for _, hk := range metrics.HotKeys {
			totalHotKeyRPS += hk.RequestsPerSecond
		}
		
		if totalHotKeyRPS/metrics.RequestsPerSecond > 0.5 { // Hot keys are >50% of load
			return types.RebalanceRecommendation{
				Action:          types.ActionTypeAddSalt,
				ShardID:         shardID,
				EstimatedImpact: totalHotKeyRPS / metrics.RequestsPerSecond,
				Risk:            types.RiskLevelLow,
				Reason:          "Hot keys detected causing load concentration",
			}
		}
	}
	
	// Check if shard should be split
	if metrics.KeyCount > d.config.MaxShardSize || sustained {
		risk := types.RiskLevelMedium
		if sustained && rpsRatio > 2.0 { // Sustained and >2x average
			risk = types.RiskLevelHigh
		}
		
		return types.RebalanceRecommendation{
			Action:          types.ActionTypeSplitShard,
			ShardID:         shardID,
			EstimatedImpact: (rpsRatio - 1.0) / rpsRatio, // Estimate load reduction
			Risk:            risk,
			Reason:          "Shard overloaded, split recommended",
		}
	}
	
	// Otherwise recommend key migration
	return types.RebalanceRecommendation{
		Action:          types.ActionTypeMigrateKeys,
		ShardID:         shardID,
		EstimatedImpact: 0.3, // Conservative estimate
		Risk:            types.RiskLevelMedium,
		Reason:          "Redistribute load via key migration",
	}
}

// DetectHotKeys detects hot keys within a shard
func (d *HotspotDetector) DetectHotKeys(shardID int32, keyMetrics map[string]float64) []types.HotKey {
	if len(keyMetrics) == 0 {
		return nil
	}
	
	// Calculate total RPS for the shard
	totalRPS := 0.0
	for _, rps := range keyMetrics {
		totalRPS += rps
	}
	
	if totalRPS == 0 {
		return nil
	}
	
	// Find keys that exceed threshold
	hotKeys := []types.HotKey{}
	for key, rps := range keyMetrics {
		percentOfShard := rps / totalRPS
		if percentOfShard > d.config.HotKeyThreshold {
			hotKeys = append(hotKeys, types.HotKey{
				Key:               key,
				RequestsPerSecond: rps,
				PercentOfShard:    percentOfShard,
			})
		}
	}
	
	// Sort by RPS descending
	sort.Slice(hotKeys, func(i, j int) bool {
		return hotKeys[i].RequestsPerSecond > hotKeys[j].RequestsPerSecond
	})
	
	return hotKeys
}

// Helper methods

func (d *HotspotDetector) updateStandardDeviation(shardID int32) {
	history := d.metricsHistory[shardID]
	if len(history) < 2 {
		return
	}
	
	// Calculate mean
	sum := 0.0
	for _, m := range history {
		sum += m.RequestsPerSecond
	}
	mean := sum / float64(len(history))
	
	// Calculate standard deviation
	sumSquares := 0.0
	for _, m := range history {
		diff := m.RequestsPerSecond - mean
		sumSquares += diff * diff
	}
	
	d.standardDev[shardID] = math.Sqrt(sumSquares / float64(len(history)))
}

func (d *HotspotDetector) detectPatterns(shardID int32) {
	history := d.metricsHistory[shardID]
	if len(history) < 10 {
		return
	}
	
	pattern, exists := d.patterns[shardID]
	if !exists {
		pattern = &LoadPattern{}
		d.patterns[shardID] = pattern
	}
	
	// Detect trend
	recentAvg := d.calculateAverage(history[len(history)-5:])
	olderAvg := d.calculateAverage(history[len(history)-10 : len(history)-5])
	
	if recentAvg > olderAvg*1.1 {
		pattern.TrendDirection = 1 // Increasing
	} else if recentAvg < olderAvg*0.9 {
		pattern.TrendDirection = -1 // Decreasing
	} else {
		pattern.TrendDirection = 0 // Stable
	}
	
	// Calculate volatility
	pattern.Volatility = d.standardDev[shardID]
	
	// Detect bursts (sudden spikes)
	latest := history[len(history)-1].RequestsPerSecond
	avg := d.movingAverage[shardID].value
	if latest > avg*2.0 { // Spike is 2x moving average
		pattern.BurstCount++
		pattern.LastBurst = time.Now()
	}
	
	// Check if sustained hot
	hotCount := 0
	threshold := avg * d.config.HotShardThreshold
	for i := len(history) - 1; i >= 0 && i >= len(history)-12; i-- { // Check last minute
		if history[i].RequestsPerSecond > threshold {
			hotCount++
		}
	}
	
	pattern.SustainedHot = hotCount >= 10 // Hot for 50+ seconds
	if pattern.SustainedHot {
		pattern.HotDuration = time.Duration(hotCount*5) * time.Second
	} else {
		pattern.HotDuration = 0
	}
}

func (d *HotspotDetector) calculateAverage(metrics []*types.ShardMetrics) float64 {
	if len(metrics) == 0 {
		return 0
	}
	
	sum := 0.0
	for _, m := range metrics {
		sum += m.RequestsPerSecond
	}
	return sum / float64(len(metrics))
}

func (d *HotspotDetector) calculateGiniCoefficient(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	
	// Sort values
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	
	// Calculate Gini coefficient
	n := len(sorted)
	sum := 0.0
	cumSum := 0.0
	
	for _, v := range sorted {
		cumSum += v
		sum += cumSum
	}
	
	if cumSum == 0 {
		return 0
	}
	
	gini := (2.0*sum)/(float64(n)*cumSum) - float64(n+1)/float64(n)
	return math.Abs(gini)
}

// ExponentialMovingAverage methods

func (ema *ExponentialMovingAverage) Update(newValue float64) {
	if ema.count == 0 {
		ema.value = newValue
	} else {
		ema.value = ema.alpha*newValue + (1-ema.alpha)*ema.value
	}
	ema.count++
}

func (ema *ExponentialMovingAverage) Value() float64 {
	return ema.value
}

// PredictiveAnalysis uses historical data to predict future hotspots
func (d *HotspotDetector) PredictiveAnalysis(shardID int32) *LoadPrediction {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	history := d.metricsHistory[shardID]
	if len(history) < 20 {
		return nil
	}
	
	pattern := d.patterns[shardID]
	if pattern == nil {
		return nil
	}
	
	prediction := &LoadPrediction{
		ShardID:          shardID,
		PredictedRPS:     d.movingAverage[shardID].value,
		ConfidenceLevel:  0.5,
		TimeHorizon:      5 * time.Minute,
	}
	
	// Adjust prediction based on trend
	if pattern.TrendDirection > 0 {
		// Increasing trend
		prediction.PredictedRPS *= 1.1
		prediction.LikelyHotspot = prediction.PredictedRPS > d.calculateSystemAverage()*d.config.HotShardThreshold
	} else if pattern.TrendDirection < 0 {
		// Decreasing trend
		prediction.PredictedRPS *= 0.9
		prediction.LikelyCold = prediction.PredictedRPS < d.calculateSystemAverage()*d.config.ColdShardThreshold
	}
	
	// Adjust confidence based on volatility
	if pattern.Volatility < 0.1 {
		prediction.ConfidenceLevel = 0.8 // Low volatility = high confidence
	} else if pattern.Volatility > 0.5 {
		prediction.ConfidenceLevel = 0.3 // High volatility = low confidence
	}
	
	// Check for periodic patterns
	if pattern.Periodicity > 0 {
		prediction.NextPeak = time.Now().Add(time.Duration(pattern.Periodicity*5) * time.Second)
	}
	
	return prediction
}

func (d *HotspotDetector) calculateSystemAverage() float64 {
	total := 0.0
	count := 0
	
	for _, ema := range d.movingAverage {
		total += ema.value
		count++
	}
	
	if count == 0 {
		return 0
	}
	
	return total / float64(count)
}

// LoadPrediction represents predicted future load
type LoadPrediction struct {
	ShardID          int32
	PredictedRPS     float64
	ConfidenceLevel  float64
	TimeHorizon      time.Duration
	LikelyHotspot    bool
	LikelyCold       bool
	NextPeak         time.Time
}