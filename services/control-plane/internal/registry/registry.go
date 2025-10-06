package registry

import (
	"fmt"
	"sync"
	"time"

	controlplane "github.com/Milad-Afdasta/TrueNow/proto/controlplane"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Registry manages service discovery state for the control plane.
type Registry struct {
	mu       sync.RWMutex
	services map[string]map[string]*serviceRecord
	idIndex  map[string]string
	ttl      time.Duration
	stopCh   chan struct{}
	once     sync.Once
}

type serviceRecord struct {
	instance *controlplane.ServiceInstance
	expires  time.Time
}

// NewRegistry creates a new in-memory registry with the provided TTL.
func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	r := &Registry{
		services: make(map[string]map[string]*serviceRecord),
		idIndex:  make(map[string]string),
		ttl:      ttl,
		stopCh:   make(chan struct{}),
	}
	go r.gc()
	return r
}

// Close stops background maintenance.
func (r *Registry) Close() {
	r.once.Do(func() {
		close(r.stopCh)
	})
}

// Register upserts a service instance and returns the canonical representation and TTL.
func (r *Registry) Register(req *controlplane.RegisterServiceRequest) (*controlplane.ServiceInstance, int32) {
	now := time.Now().UTC()
	expires := now.Add(r.ttl)

	metadata := map[string]string{}
	for k, v := range req.GetMetadata() {
		metadata[k] = v
	}

	health := req.GetHealth()
	if health == nil {
		health = &controlplane.ServiceHealth{Status: controlplane.HealthStatus_HEALTHY}
	}
	if health.GetLastCheck() == nil {
		health.LastCheck = timestamppb.New(now)
	}

	inst := &controlplane.ServiceInstance{
		ServiceId:     req.GetServiceId(),
		ServiceType:   req.GetServiceType(),
		Host:          req.GetHost(),
		Port:          req.GetPort(),
		Metadata:      metadata,
		Health:        health,
		RegisteredAt:  timestamppb.New(now),
		LastHeartbeat: timestamppb.New(now),
	}
	if existing := r.get(req.GetServiceType(), req.GetServiceId()); existing != nil {
		inst.RegisteredAt = existing.instance.GetRegisteredAt()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	typeMap, ok := r.services[req.GetServiceType()]
	if !ok {
		typeMap = make(map[string]*serviceRecord)
		r.services[req.GetServiceType()] = typeMap
	}
	typeMap[req.GetServiceId()] = &serviceRecord{
		instance: inst,
		expires:  expires,
	}
	r.idIndex[req.GetServiceId()] = req.GetServiceType()

	ttlSeconds := int32(r.ttl / time.Second)
	if ttlSeconds <= 0 {
		ttlSeconds = 1
	}

	return inst, ttlSeconds
}

// Unregister removes the service from the registry.
func (r *Registry) Unregister(serviceType, serviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if serviceType == "" {
		serviceType = r.idIndex[serviceID]
	}

	typeMap, ok := r.services[serviceType]
	if !ok {
		return false
	}
	if _, ok := typeMap[serviceID]; !ok {
		return false
	}
	delete(typeMap, serviceID)
	if len(typeMap) == 0 {
		delete(r.services, serviceType)
	}
	delete(r.idIndex, serviceID)
	return true
}

// List returns all registered services with optional filters and pagination.
func (r *Registry) List(serviceType string, status controlplane.HealthStatus, limit, offset int) ([]*controlplane.ServiceInstance, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []*controlplane.ServiceInstance

	appendIfMatch := func(records map[string]*serviceRecord) {
		for _, rec := range records {
			if status != controlplane.HealthStatus_UNKNOWN && rec.instance.GetHealth().GetStatus() != status {
				continue
			}
			all = append(all, rec.instance)
		}
	}

	if serviceType != "" {
		if records, ok := r.services[serviceType]; ok {
			appendIfMatch(records)
		}
	} else {
		for _, records := range r.services {
			appendIfMatch(records)
		}
	}

	total := len(all)
	if offset >= total {
		return []*controlplane.ServiceInstance{}, total
	}

	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}

	result := make([]*controlplane.ServiceInstance, limit)
	copy(result, all[offset:offset+limit])
	return result, total
}

// Endpoints returns service endpoints for a given type, optionally filtering on health.
func (r *Registry) Endpoints(serviceType string, healthyOnly bool) []*controlplane.Endpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	records, ok := r.services[serviceType]
	if !ok {
		return nil
	}

	endpoints := make([]*controlplane.Endpoint, 0, len(records))
	for _, rec := range records {
		status := rec.instance.GetHealth().GetStatus()
		if healthyOnly && status != controlplane.HealthStatus_HEALTHY {
			continue
		}
		url := buildEndpointURL(rec.instance)
		endpoints = append(endpoints, &controlplane.Endpoint{
			ServiceId: rec.instance.GetServiceId(),
			Url:       url,
			Metadata:  rec.instance.GetMetadata(),
			Health:    rec.instance.GetHealth(),
			Weight:    1,
		})
	}
	return endpoints
}

func (r *Registry) get(serviceType, serviceID string) *serviceRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if records, ok := r.services[serviceType]; ok {
		return records[serviceID]
	}
	return nil
}

func (r *Registry) gc() {
	ticker := time.NewTicker(r.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.expireStale()
		case <-r.stopCh:
			return
		}
	}
}

func (r *Registry) expireStale() {
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()

	for serviceType, records := range r.services {
		for id, rec := range records {
			if rec.expires.Before(now) {
				delete(records, id)
				delete(r.idIndex, id)
			}
		}
		if len(records) == 0 {
			delete(r.services, serviceType)
		}
	}
}

func buildEndpointURL(inst *controlplane.ServiceInstance) string {
	scheme := inst.GetMetadata()["scheme"]
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + inst.GetHost() + ":" + fmt.Sprint(inst.GetPort())
}
