package ringbuffer

import (
	"math"
	"sort"
	"sync"
)

// TDigest is a data structure for accurate online accumulation of rank-based statistics
// such as quantiles and percentiles with bounded memory usage
type TDigest struct {
	compression float64
	centroids   []Centroid
	count       float64
	min         float64
	max         float64
	mu          sync.RWMutex
}

// Centroid represents a cluster of values
type Centroid struct {
	Mean  float64
	Count float64
}

// NewTDigest creates a new T-Digest with given compression
// Higher compression = better accuracy but more memory
func NewTDigest(compression float64) *TDigest {
	if compression < 10 {
		compression = 10
	} else if compression > 1000 {
		compression = 1000
	}
	
	return &TDigest{
		compression: compression,
		centroids:   make([]Centroid, 0, int(compression*2)),
		min:         math.Inf(1),
		max:         math.Inf(-1),
	}
}

// Add adds a value to the T-Digest
func (t *TDigest) Add(value float64, weight float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if weight <= 0 {
		return
	}
	
	// Update min/max
	if value < t.min {
		t.min = value
	}
	if value > t.max {
		t.max = value
	}
	
	// Add to centroids
	t.addCentroid(Centroid{Mean: value, Count: weight})
	t.count += weight
	
	// Compress if needed
	if len(t.centroids) > int(t.compression*2) {
		t.compress()
	}
}

// AddValue adds a single value with weight 1
func (t *TDigest) AddValue(value float64) {
	t.Add(value, 1.0)
}

// Quantile returns the value at the given quantile (0-1)
func (t *TDigest) Quantile(q float64) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	if q < 0 || q > 1 {
		return math.NaN()
	}
	
	if t.count == 0 {
		return math.NaN()
	}
	
	if q == 0 {
		return t.min
	}
	if q == 1 {
		return t.max
	}
	
	if len(t.centroids) == 1 {
		return t.centroids[0].Mean
	}
	
	// Find the centroid containing the quantile
	targetCount := q * t.count
	sum := 0.0
	
	for i, c := range t.centroids {
		sum += c.Count
		
		if sum >= targetCount {
			// Interpolate within the centroid
			if i == 0 {
				return c.Mean
			}
			
			prevSum := sum - c.Count
			fraction := (targetCount - prevSum) / c.Count
			
			if i == len(t.centroids)-1 {
				return c.Mean
			}
			
			// Linear interpolation between centroids
			nextMean := t.centroids[i+1].Mean
			return c.Mean + fraction*(nextMean-c.Mean)
		}
	}
	
	return t.centroids[len(t.centroids)-1].Mean
}

// Percentile returns the value at the given percentile (0-100)
func (t *TDigest) Percentile(p float64) float64 {
	return t.Quantile(p / 100.0)
}

// CDF returns the cumulative distribution function value for x
func (t *TDigest) CDF(x float64) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	if t.count == 0 {
		return 0
	}
	
	if x < t.min {
		return 0
	}
	if x > t.max {
		return 1
	}
	
	sum := 0.0
	for _, c := range t.centroids {
		if c.Mean >= x {
			break
		}
		sum += c.Count
	}
	
	return sum / t.count
}

// Merge combines another T-Digest into this one
func (t *TDigest) Merge(other *TDigest) {
	other.mu.RLock()
	otherCentroids := make([]Centroid, len(other.centroids))
	copy(otherCentroids, other.centroids)
	otherMin := other.min
	otherMax := other.max
	other.mu.RUnlock()
	
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Add all centroids from other
	for _, c := range otherCentroids {
		t.addCentroid(c)
	}
	
	// Update min/max
	if otherMin < t.min {
		t.min = otherMin
	}
	if otherMax > t.max {
		t.max = otherMax
	}
	
	t.compress()
}

// Clone creates a copy of the T-Digest
func (t *TDigest) Clone() *TDigest {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	clone := &TDigest{
		compression: t.compression,
		centroids:   make([]Centroid, len(t.centroids)),
		count:       t.count,
		min:         t.min,
		max:         t.max,
	}
	
	copy(clone.centroids, t.centroids)
	
	return clone
}

// addCentroid adds a centroid to the list
func (t *TDigest) addCentroid(c Centroid) {
	if len(t.centroids) == 0 {
		t.centroids = append(t.centroids, c)
		return
	}
	
	// Find insertion point using binary search
	idx := sort.Search(len(t.centroids), func(i int) bool {
		return t.centroids[i].Mean >= c.Mean
	})
	
	// Check if we can merge with existing centroid
	merged := false
	
	if idx < len(t.centroids) && math.Abs(t.centroids[idx].Mean-c.Mean) < 1e-10 {
		// Merge with existing centroid at same mean
		t.centroids[idx].Count += c.Count
		merged = true
	} else if idx > 0 && math.Abs(t.centroids[idx-1].Mean-c.Mean) < 1e-10 {
		// Merge with previous centroid
		t.centroids[idx-1].Count += c.Count
		merged = true
	}
	
	if !merged {
		// Insert new centroid
		t.centroids = append(t.centroids, Centroid{})
		copy(t.centroids[idx+1:], t.centroids[idx:])
		t.centroids[idx] = c
	}
}

// compress merges centroids to maintain size bounds
func (t *TDigest) compress() {
	if len(t.centroids) <= 1 {
		return
	}
	
	// Sort centroids by mean
	sort.Slice(t.centroids, func(i, j int) bool {
		return t.centroids[i].Mean < t.centroids[j].Mean
	})
	
	// Merge adjacent centroids based on size constraints
	merged := make([]Centroid, 0, len(t.centroids))
	
	sum := 0.0
	for _, c := range t.centroids {
		if len(merged) == 0 {
			merged = append(merged, c)
			sum = c.Count
		} else {
			// Calculate size bound for this position
			q := sum / t.count
			k := 4 * t.compression * q * (1 - q)
			maxSize := t.count * k / t.compression
			
			last := &merged[len(merged)-1]
			if last.Count+c.Count <= maxSize {
				// Merge with previous centroid
				totalCount := last.Count + c.Count
				last.Mean = (last.Mean*last.Count + c.Mean*c.Count) / totalCount
				last.Count = totalCount
			} else {
				// Add as new centroid
				merged = append(merged, c)
			}
			sum += c.Count
		}
	}
	
	t.centroids = merged
}

// Count returns the total count of values
func (t *TDigest) Count() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// Min returns the minimum value seen
func (t *TDigest) Min() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.count == 0 {
		return math.NaN()
	}
	return t.min
}

// Max returns the maximum value seen
func (t *TDigest) Max() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.count == 0 {
		return math.NaN()
	}
	return t.max
}

// Mean returns the mean of all values
func (t *TDigest) Mean() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	if t.count == 0 {
		return math.NaN()
	}
	
	sum := 0.0
	for _, c := range t.centroids {
		sum += c.Mean * c.Count
	}
	
	return sum / t.count
}