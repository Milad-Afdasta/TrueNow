package grpc

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/timestamppb"
	
	pb "github.com/Milad-Afdasta/TrueNow/services/autoscaler/proto"
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/autoscaler"
)

// Server implements the gRPC AutoscalerService
type Server struct {
	pb.UnimplementedAutoscalerServiceServer
	
	autoscaler *autoscaler.FixedAutoScaler
	port       int
}

// NewServer creates a new gRPC server
func NewServer(autoscaler *autoscaler.FixedAutoScaler, port int) *Server {
	return &Server{
		autoscaler: autoscaler,
		port:       port,
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	
	grpcServer := grpc.NewServer()
	pb.RegisterAutoscalerServiceServer(grpcServer, s)
	
	// Register reflection service for grpcurl
	reflection.Register(grpcServer)
	
	log.Printf("gRPC server starting on port %d", s.port)
	return grpcServer.Serve(lis)
}

// GetStatus returns the current status of all services
func (s *Server) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	status := s.autoscaler.GetRealStatus()
	
	// Convert to protobuf format
	services := make(map[string]*pb.ServiceStatus)
	
	if svcMap, ok := status["services"].(map[string]interface{}); ok {
		for name, svcData := range svcMap {
			if svc, ok := svcData.(map[string]interface{}); ok {
				pbStatus := &pb.ServiceStatus{
					Name:             name,
					CurrentInstances: int32(svc["current_instances"].(int)),
					MinInstances:     int32(svc["min_instances"].(int)),
					MaxInstances:     int32(svc["max_instances"].(int)),
					State:            pb.ServiceState_IDLE,
				}
				
				// Add instances
				if instances, ok := svc["instances"].([]interface{}); ok {
					for _, inst := range instances {
						if instMap, ok := inst.(map[string]interface{}); ok {
							pbInst := &pb.Instance{
								Id:          instMap["id"].(string),
								ServiceName: name,
								Status:      pb.InstanceStatus_RUNNING,
								Port:        int32(instMap["port"].(int)),
								StartedAt:   timestamppb.Now(),
							}
							pbStatus.Instances = append(pbStatus.Instances, pbInst)
						}
					}
				}
				
				// Add metrics
				if metrics, ok := svc["metrics"].(map[string]float64); ok {
					pbStatus.Metrics = metrics
				}
				
				services[name] = pbStatus
			}
		}
	}
	
	// Calculate cluster status
	cluster := &pb.ClusterStatus{
		TotalInstances: int32(status["total_instances"].(int)),
		LoadFactor:     0.5, // Default
	}
	
	return &pb.GetStatusResponse{
		Running:    status["running"].(bool),
		Services:   services,
		Cluster:    cluster,
		LastUpdate: timestamppb.Now(),
	}, nil
}

// StreamStatus streams real-time status updates
func (s *Server) StreamStatus(req *pb.StreamStatusRequest, stream pb.AutoscalerService_StreamStatusServer) error {
	interval := time.Duration(req.IntervalMs) * time.Millisecond
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			status := s.autoscaler.GetRealStatus()
			
			// Send cluster update
			if svcMap, ok := status["services"].(map[string]interface{}); ok {
				update := &pb.StatusUpdate{
					Update: &pb.StatusUpdate_ClusterUpdate{
						ClusterUpdate: &pb.ClusterStatus{
							TotalInstances: int32(status["total_instances"].(int)),
						},
					},
					Timestamp: timestamppb.Now(),
				}
				
				if err := stream.Send(update); err != nil {
					return err
				}
				
				// Send service updates
				for name, svcData := range svcMap {
					if svc, ok := svcData.(map[string]interface{}); ok {
						serviceUpdate := &pb.ServiceStatus{
							Name:             name,
							CurrentInstances: int32(svc["current_instances"].(int)),
						}
						
						update := &pb.StatusUpdate{
							Update: &pb.StatusUpdate_ServiceUpdate{
								ServiceUpdate: serviceUpdate,
							},
							Timestamp: timestamppb.Now(),
						}
						
						if err := stream.Send(update); err != nil {
							return err
						}
					}
				}
			}
		}
	}
}

// GetServiceMetrics returns metrics for a specific service
func (s *Server) GetServiceMetrics(ctx context.Context, req *pb.GetServiceMetricsRequest) (*pb.ServiceMetrics, error) {
	// Simplified - return mock metrics
	return &pb.ServiceMetrics{
		ServiceName: req.ServiceName,
		Cpu: []*pb.MetricPoint{
			{Value: 45.5, Timestamp: timestamppb.Now()},
		},
		Memory: []*pb.MetricPoint{
			{Value: 60.2, Timestamp: timestamppb.Now()},
		},
	}, nil
}

// ScaleService manually scales a service
func (s *Server) ScaleService(ctx context.Context, req *pb.ScaleServiceRequest) (*pb.ScaleServiceResponse, error) {
	// This would trigger manual scaling
	return &pb.ScaleServiceResponse{
		Success:          true,
		Message:          "Scaling initiated",
		TargetInstances:  req.TargetInstances,
	}, nil
}

// GetScalingHistory returns scaling history
func (s *Server) GetScalingHistory(ctx context.Context, req *pb.GetScalingHistoryRequest) (*pb.GetScalingHistoryResponse, error) {
	// Return empty history for now
	return &pb.GetScalingHistoryResponse{
		Events: []*pb.ScalingEvent{},
	}, nil
}