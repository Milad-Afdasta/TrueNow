package ringbuffer

import (
	"hash/fnv"
	"math"
	"sync"
)

// HyperLogLog is a probabilistic data structure for cardinality estimation
// This implementation is optimized for performance with minimal memory usage
type HyperLogLog struct {
	m        uint32    // Number of registers (2^precision)
	p        uint8     // Precision (4-16)
	registers []uint8  // Register array
	mu       sync.RWMutex // For thread safety on updates
}

// NewHyperLogLog creates a new HyperLogLog with given precision
// precision: 4-16, higher = more accurate but more memory
// precision 14 = 16KB memory, 0.8% error
func NewHyperLogLog(precision uint8) *HyperLogLog {
	if precision < 4 {
		precision = 4
	} else if precision > 16 {
		precision = 16
	}
	
	m := uint32(1) << precision
	
	return &HyperLogLog{
		m:         m,
		p:         precision,
		registers: make([]uint8, m),
	}
}

// Add adds an element to the HyperLogLog
func (h *HyperLogLog) Add(data []byte) {
	hash := fnv.New64a()
	hash.Write(data)
	x := hash.Sum64()
	
	// Use first p bits as register index
	j := x >> (64 - h.p)
	
	// Count leading zeros in remaining bits + 1
	w := x << h.p
	rho := leadingZeros(w) + 1
	
	// Update register if new value is larger
	h.mu.Lock()
	if rho > h.registers[j] {
		h.registers[j] = rho
	}
	h.mu.Unlock()
}

// AddString is a convenience method for adding strings
func (h *HyperLogLog) AddString(s string) {
	h.Add([]byte(s))
}

// Estimate returns the estimated cardinality
func (h *HyperLogLog) Estimate() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	// Calculate raw estimate using harmonic mean
	sum := 0.0
	zeros := uint32(0)
	
	for _, val := range h.registers {
		if val == 0 {
			zeros++
		}
		sum += 1.0 / float64(uint64(1)<<val)
	}
	
	m := float64(h.m)
	estimate := alpha(h.m) * m * m / sum
	
	// Apply bias correction for small and large cardinalities
	if estimate <= 2.5*m {
		// Small range correction
		if zeros != 0 {
			estimate = m * math.Log(m/float64(zeros))
		}
	} else if estimate > (1.0/30.0)*math.Pow(2, 32) {
		// Large range correction
		estimate = -math.Pow(2, 32) * math.Log(1-estimate/math.Pow(2, 32))
	}
	
	return uint64(estimate)
}

// Merge combines another HyperLogLog into this one
func (h *HyperLogLog) Merge(other *HyperLogLog) error {
	if h.m != other.m {
		return ErrIncompatiblePrecision
	}
	
	h.mu.Lock()
	other.mu.RLock()
	defer h.mu.Unlock()
	defer other.mu.RUnlock()
	
	for i := uint32(0); i < h.m; i++ {
		if other.registers[i] > h.registers[i] {
			h.registers[i] = other.registers[i]
		}
	}
	
	return nil
}

// Clone creates a copy of the HyperLogLog
func (h *HyperLogLog) Clone() *HyperLogLog {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	clone := &HyperLogLog{
		m:         h.m,
		p:         h.p,
		registers: make([]uint8, h.m),
	}
	
	copy(clone.registers, h.registers)
	
	return clone
}

// Reset clears the HyperLogLog
func (h *HyperLogLog) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	for i := range h.registers {
		h.registers[i] = 0
	}
}

// leadingZeros counts the number of leading zero bits
func leadingZeros(x uint64) uint8 {
	if x == 0 {
		return 64
	}
	
	n := uint8(0)
	if x <= 0x00000000FFFFFFFF {
		n += 32
		x <<= 32
	}
	if x <= 0x0000FFFFFFFFFFFF {
		n += 16
		x <<= 16
	}
	if x <= 0x00FFFFFFFFFFFFFF {
		n += 8
		x <<= 8
	}
	if x <= 0x0FFFFFFFFFFFFFFF {
		n += 4
		x <<= 4
	}
	if x <= 0x3FFFFFFFFFFFFFFF {
		n += 2
		x <<= 2
	}
	if x <= 0x7FFFFFFFFFFFFFFF {
		n += 1
	}
	
	return n
}

// alpha returns the bias correction constant
func alpha(m uint32) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/float64(m))
	}
}

// Error types
type Error string

func (e Error) Error() string {
	return string(e)
}

const (
	ErrIncompatiblePrecision = Error("incompatible precision for merge")
)