package grpcapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	controlplane "github.com/Milad-Afdasta/TrueNow/proto/controlplane"
	"github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/audit"
	"github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/registry"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultQueryTimeout = 2 * time.Second

// Server implements the ControlPlaneService gRPC contract.
type Server struct {
	controlplane.UnimplementedControlPlaneServiceServer

	db           *sql.DB
	auditor      *audit.Auditor
	registry     *registry.Registry
	queryTimeout time.Duration
}

// NewServer constructs a new gRPC server wrapper.
func NewServer(db *sql.DB, auditor *audit.Auditor, reg *registry.Registry) *Server {
	return &Server{
		db:           db,
		auditor:      auditor,
		registry:     reg,
		queryTimeout: defaultQueryTimeout,
	}
}

// RegisterService registers or refreshes a service endpoint.
func (s *Server) RegisterService(ctx context.Context, req *controlplane.RegisterServiceRequest) (*controlplane.RegisterServiceResponse, error) {
	if req.GetServiceId() == "" || req.GetServiceType() == "" {
		return nil, status.Error(codes.InvalidArgument, "service_id and service_type are required")
	}
	if req.GetHost() == "" || req.GetPort() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "valid host and port are required")
	}

	instance, ttl := s.registry.Register(req)
	log.WithFields(log.Fields{
		"service_id":   instance.GetServiceId(),
		"service_type": instance.GetServiceType(),
		"host":         instance.GetHost(),
		"port":         instance.GetPort(),
	}).Debug("control-plane: service registered")

	return &controlplane.RegisterServiceResponse{
		Success:    true,
		Message:    "registered",
		TtlSeconds: ttl,
	}, nil
}

// UnregisterService removes a service entry from the registry.
func (s *Server) UnregisterService(ctx context.Context, req *controlplane.UnregisterServiceRequest) (*controlplane.UnregisterServiceResponse, error) {
	if req.GetServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "service_id is required")
	}

	removed := s.registry.Unregister("", req.GetServiceId())
	if !removed {
		return &controlplane.UnregisterServiceResponse{Success: false, Message: "service not found"}, nil
	}

	return &controlplane.UnregisterServiceResponse{Success: true, Message: "unregistered"}, nil
}

// ListServices returns registered services.
func (s *Server) ListServices(ctx context.Context, req *controlplane.ListServicesRequest) (*controlplane.ListServicesResponse, error) {
	limit := int(req.GetLimit())
	offset := int(req.GetOffset())
	services, total := s.registry.List(req.GetServiceType(), req.GetHealthFilter(), limit, offset)
	return &controlplane.ListServicesResponse{Services: services, TotalCount: int32(total)}, nil
}

// GetServiceEndpoints returns endpoints for a service type.
func (s *Server) GetServiceEndpoints(ctx context.Context, req *controlplane.GetServiceEndpointsRequest) (*controlplane.GetServiceEndpointsResponse, error) {
	if req.GetServiceType() == "" {
		return nil, status.Error(codes.InvalidArgument, "service_type is required")
	}
	endpoints := s.registry.Endpoints(req.GetServiceType(), req.GetHealthyOnly())
	return &controlplane.GetServiceEndpointsResponse{Endpoints: endpoints}, nil
}

// GetCurrentEpoch retrieves the latest epoch metadata for a namespace.
func (s *Server) GetCurrentEpoch(ctx context.Context, req *controlplane.GetCurrentEpochRequest) (*controlplane.GetCurrentEpochResponse, error) {
	if req.GetNamespace() == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	const stmt = `
	SELECT e.epoch_number, COALESCE(tv.version, 0) AS version, e.created_at
	FROM epochs e
	JOIN table_versions tv ON e.table_version_id = tv.id
	JOIN tables t ON tv.table_id = t.id
	JOIN namespaces n ON t.namespace_id = n.id
	WHERE n.name = $1
	ORDER BY e.epoch_number DESC
	LIMIT 1`

	var (
		epochNumber int64
		version     int64
		createdAt   time.Time
	)

	err := s.db.QueryRowContext(queryCtx, stmt, req.GetNamespace()).Scan(&epochNumber, &version, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &controlplane.GetCurrentEpochResponse{
			Epoch: &controlplane.Epoch{
				EpochId:   1,
				Version:   1,
				State:     controlplane.EpochState_ACTIVE,
				StartedAt: timestamppb.Now(),
			},
		}, nil
	}
	if err != nil {
		log.WithError(err).Error("control-plane: failed to load current epoch")
		return nil, status.Error(codes.Internal, "failed to load epoch")
	}

	return &controlplane.GetCurrentEpochResponse{
		Epoch: &controlplane.Epoch{
			EpochId:   epochNumber,
			Version:   version,
			State:     controlplane.EpochState_ACTIVE,
			StartedAt: timestamppb.New(createdAt),
		},
	}, nil
}

// The remaining schema/config RPCs are stubs until full implementations land.

func (s *Server) CreateSchema(ctx context.Context, req *controlplane.CreateSchemaRequest) (*controlplane.CreateSchemaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "CreateSchema not yet implemented")
}

func (s *Server) GetSchema(ctx context.Context, req *controlplane.GetSchemaRequest) (*controlplane.GetSchemaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetSchema not yet implemented")
}

func (s *Server) UpdateSchema(ctx context.Context, req *controlplane.UpdateSchemaRequest) (*controlplane.UpdateSchemaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "UpdateSchema not yet implemented")
}

func (s *Server) ListSchemas(ctx context.Context, req *controlplane.ListSchemasRequest) (*controlplane.ListSchemasResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListSchemas not yet implemented")
}

func (s *Server) ProposeEpochTransition(ctx context.Context, req *controlplane.ProposeEpochTransitionRequest) (*controlplane.ProposeEpochTransitionResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ProposeEpochTransition not yet implemented")
}

func (s *Server) CommitEpoch(ctx context.Context, req *controlplane.CommitEpochRequest) (*controlplane.CommitEpochResponse, error) {
	return nil, status.Error(codes.Unimplemented, "CommitEpoch not yet implemented")
}

func (s *Server) GetConfig(ctx context.Context, req *controlplane.GetConfigRequest) (*controlplane.GetConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetConfig not yet implemented")
}

func (s *Server) UpdateConfig(ctx context.Context, req *controlplane.UpdateConfigRequest) (*controlplane.UpdateConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "UpdateConfig not yet implemented")
}

func (s *Server) GetAuditLog(ctx context.Context, req *controlplane.GetAuditLogRequest) (*controlplane.GetAuditLogResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetAuditLog not yet implemented")
}

func (s *Server) RecordAuditEvent(ctx context.Context, req *controlplane.RecordAuditEventRequest) (*controlplane.RecordAuditEventResponse, error) {
	return nil, status.Error(codes.Unimplemented, "RecordAuditEvent not yet implemented")
}
