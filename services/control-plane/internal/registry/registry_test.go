package registry

import (
	"testing"
	"time"

	controlplane "github.com/Milad-Afdasta/TrueNow/proto/controlplane"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRegistryRegisterListAndExpire(t *testing.T) {
	r := NewRegistry(50 * time.Millisecond)
	defer r.Close()

	req := &controlplane.RegisterServiceRequest{
		ServiceId:   "svc-1",
		ServiceType: "query-api",
		Host:        "127.0.0.1",
		Port:        8081,
		Metadata:    map[string]string{"scheme": "http"},
		Health: &controlplane.ServiceHealth{
			Status:    controlplane.HealthStatus_HEALTHY,
			LastCheck: timestamppb.Now(),
		},
	}

	inst, ttl := r.Register(req)
	if inst.GetServiceId() != req.ServiceId {
		t.Fatalf("expected service ID %s, got %s", req.ServiceId, inst.GetServiceId())
	}
	if ttl <= 0 {
		t.Fatal("expected positive TTL")
	}

	services, total := r.List("", controlplane.HealthStatus_UNKNOWN, 10, 0)
	if total != 1 || len(services) != 1 {
		t.Fatalf("expected 1 service, got total=%d len=%d", total, len(services))
	}

	endpoints := r.Endpoints("query-api", true)
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].GetUrl() != "http://127.0.0.1:8081" {
		t.Fatalf("unexpected endpoint url %s", endpoints[0].GetUrl())
	}

	// Ensure expiration clears the service
	time.Sleep(150 * time.Millisecond)
	if len(r.Endpoints("query-api", false)) != 0 {
		t.Fatal("expected endpoints to expire")
	}
}
