package controller

import (
	"fmt"
	"net"
	"sync"
)

// PortAllocator manages port allocation for service instances
type PortAllocator struct {
	minPort   int
	maxPort   int
	allocated map[int]bool
	mu        sync.Mutex
}

// NewPortAllocator creates a new port allocator
func NewPortAllocator(minPort, maxPort int) *PortAllocator {
	return &PortAllocator{
		minPort:   minPort,
		maxPort:   maxPort,
		allocated: make(map[int]bool),
	}
}

// Allocate allocates a free port
func (p *PortAllocator) Allocate() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	for port := p.minPort; port <= p.maxPort; port++ {
		// Skip if already allocated
		if p.allocated[port] {
			continue
		}
		
		// Check if port is available
		if p.isPortAvailable(port) {
			p.allocated[port] = true
			return port, nil
		}
	}
	
	return 0, fmt.Errorf("no available ports in range %d-%d", p.minPort, p.maxPort)
}

// AllocateSpecific allocates a specific port if available
func (p *PortAllocator) AllocateSpecific(port int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if port < p.minPort || port > p.maxPort {
		return fmt.Errorf("port %d is outside allowed range %d-%d", port, p.minPort, p.maxPort)
	}
	
	if p.allocated[port] {
		return fmt.Errorf("port %d is already allocated", port)
	}
	
	if !p.isPortAvailable(port) {
		return fmt.Errorf("port %d is not available", port)
	}
	
	p.allocated[port] = true
	return nil
}

// Release releases an allocated port
func (p *PortAllocator) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	delete(p.allocated, port)
}

// IsAllocated checks if a port is allocated
func (p *PortAllocator) IsAllocated(port int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	return p.allocated[port]
}

// GetAllocated returns all allocated ports
func (p *PortAllocator) GetAllocated() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	ports := make([]int, 0, len(p.allocated))
	for port := range p.allocated {
		ports = append(ports, port)
	}
	
	return ports
}

// Reset resets all allocations
func (p *PortAllocator) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	p.allocated = make(map[int]bool)
}

// isPortAvailable checks if a port is available for binding
func (p *PortAllocator) isPortAvailable(port int) bool {
	// Try to listen on the port
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// GetAvailableCount returns the number of available ports
func (p *PortAllocator) GetAvailableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	total := p.maxPort - p.minPort + 1
	return total - len(p.allocated)
}