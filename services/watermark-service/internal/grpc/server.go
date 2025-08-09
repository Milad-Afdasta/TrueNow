package grpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/Milad-Afdasta/TrueNow/proto/watermark"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/aggregator"
	"github.com/Milad-Afdasta/TrueNow/services/watermark-service/internal/store"
)

// Server implements the gRPC WatermarkService
type Server struct {
	pb.UnimplementedWatermarkServiceServer
	
	store      *store.WatermarkStore
	aggregator *aggregator.WatermarkAggregator
	logger     *logrus.Logger
	port       int
	
	// For streaming
	streamClients map[string]chan *pb.WatermarkUpdate
	streamMu      sync.RWMutex
}

// NewServer creates a new gRPC server
func NewServer(
	store *store.WatermarkStore,
	aggregator *aggregator.WatermarkAggregator,
	logger *logrus.Logger,
	port int,
) *Server {
	return &Server{
		store:         store,
		aggregator:    aggregator,
		logger:        logger,
		port:          port,
		streamClients: make(map[string]chan *pb.WatermarkUpdate),
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	
	grpcServer := grpc.NewServer()
	pb.RegisterWatermarkServiceServer(grpcServer, s)
	
	// Register reflection service for grpcurl
	reflection.Register(grpcServer)
	
	s.logger.WithField("port", s.port).Info("Starting gRPC server")
	return grpcServer.Serve(lis)
}

// ReportWatermark handles a single watermark report
func (s *Server) ReportWatermark(ctx context.Context, req *pb.ReportWatermarkRequest) (*pb.ReportWatermarkResponse, error) {
	// Update the store
	s.store.UpdateWatermark(
		req.Service,
		req.Namespace,
		req.Table,
		req.WatermarkMs,
		req.Labels,
	)
	
	// Broadcast to stream clients
	s.broadcastUpdate(&pb.WatermarkUpdate{
		Service:     req.Service,
		Namespace:   req.Namespace,
		Table:       req.Table,
		WatermarkMs: req.WatermarkMs,
		FreshnessMs: time.Now().UnixMilli() - req.WatermarkMs,
		Timestamp:   timestamppb.Now(),
	})
	
	return &pb.ReportWatermarkResponse{
		Success: true,
		Message: "Watermark updated",
	}, nil
}

// BatchReportWatermarks handles batch watermark reports for efficiency at scale
func (s *Server) BatchReportWatermarks(ctx context.Context, req *pb.BatchReportWatermarksRequest) (*pb.BatchReportWatermarksResponse, error) {
	succeeded := 0
	failed := 0
	var errors []string
	
	for _, watermark := range req.Watermarks {
		// Update the store
		s.store.UpdateWatermark(
			watermark.Service,
			watermark.Namespace,
			watermark.Table,
			watermark.WatermarkMs,
			watermark.Labels,
		)
		succeeded++
		
		// Broadcast to stream clients
		s.broadcastUpdate(&pb.WatermarkUpdate{
			Service:     watermark.Service,
			Namespace:   watermark.Namespace,
			Table:       watermark.Table,
			WatermarkMs: watermark.WatermarkMs,
			FreshnessMs: time.Now().UnixMilli() - watermark.WatermarkMs,
			Timestamp:   timestamppb.Now(),
		})
	}
	
	return &pb.BatchReportWatermarksResponse{
		Succeeded: int32(succeeded),
		Failed:    int32(failed),
		Errors:    errors,
	}, nil
}

// StreamWatermarks streams real-time watermark updates
func (s *Server) StreamWatermarks(req *pb.StreamWatermarksRequest, stream pb.WatermarkService_StreamWatermarksServer) error {
	// Create channel for this client
	clientID := fmt.Sprintf("%d", time.Now().UnixNano())
	updateChan := make(chan *pb.WatermarkUpdate, 100)
	
	s.streamMu.Lock()
	s.streamClients[clientID] = updateChan
	s.streamMu.Unlock()
	
	defer func() {
		s.streamMu.Lock()
		delete(s.streamClients, clientID)
		s.streamMu.Unlock()
		close(updateChan)
	}()
	
	// Set up interval
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
			
		case update := <-updateChan:
			// Filter by namespace/table if specified
			if len(req.Namespaces) > 0 && !contains(req.Namespaces, update.Namespace) {
				continue
			}
			if len(req.Tables) > 0 && !contains(req.Tables, update.Table) {
				continue
			}
			
			if err := stream.Send(update); err != nil {
				return err
			}
			
		case <-ticker.C:
			// Send periodic updates of all watermarks
			watermarks := s.store.GetAllWatermarks()
			for _, wm := range watermarks {
				// Filter by namespace/table if specified
				if len(req.Namespaces) > 0 && !contains(req.Namespaces, wm.Namespace) {
					continue
				}
				if len(req.Tables) > 0 && !contains(req.Tables, wm.Table) {
					continue
				}
				
				update := &pb.WatermarkUpdate{
					Service:     wm.ServiceID,
					Namespace:   wm.Namespace,
					Table:       wm.Table,
					WatermarkMs: wm.HighWatermark,
					FreshnessMs: time.Now().UnixMilli() - wm.HighWatermark,
					Timestamp:   timestamppb.Now(),
				}
				
				if err := stream.Send(update); err != nil {
					return err
				}
			}
		}
	}
}

// GetGlobalFreshness returns global freshness metrics
func (s *Server) GetGlobalFreshness(ctx context.Context, req *pb.GetGlobalFreshnessRequest) (*pb.GlobalFreshnessResponse, error) {
	stats := s.store.GetGlobalStats()
	
	return &pb.GlobalFreshnessResponse{
		GlobalFreshnessP50: 0, // Need to calculate from table watermarks
		GlobalFreshnessP95: 0,
		GlobalFreshnessP99: 0,
		TotalTables:        int32(stats.TotalTables),
		StaleTables:        int32(stats.TotalViolations),
		LastUpdate:         timestamppb.Now(),
	}, nil
}

// GetTableFreshness returns freshness for a specific table
func (s *Server) GetTableFreshness(ctx context.Context, req *pb.GetTableFreshnessRequest) (*pb.TableFreshnessResponse, error) {
	watermarks := s.store.GetTableWatermarks(req.Namespace, req.Table)
	
	// Calculate percentiles
	var freshness []int64
	var serviceWatermarks []*pb.ServiceWatermark
	
	if watermarks != nil {
		for _, wm := range watermarks.ShardWatermarks {
			freshnessMs := time.Now().UnixMilli() - wm.HighWatermark
			freshness = append(freshness, freshnessMs)
			
			serviceWatermarks = append(serviceWatermarks, &pb.ServiceWatermark{
				Service:     wm.ServiceID,
				WatermarkMs: wm.HighWatermark,
				FreshnessMs: freshnessMs,
				ReportedAt:  timestamppb.New(wm.LastUpdate),
			})
		}
	}
	
	p50, p95, p99 := calculatePercentiles(freshness)
	
	return &pb.TableFreshnessResponse{
		Namespace:         req.Namespace,
		Table:             req.Table,
		FreshnessP50:      p50,
		FreshnessP95:      p95,
		FreshnessP99:      p99,
		ServiceWatermarks: serviceWatermarks,
		LastUpdate:        timestamppb.Now(),
	}, nil
}

// GetSLOViolations returns current SLO violations
func (s *Server) GetSLOViolations(ctx context.Context, req *pb.GetSLOViolationsRequest) (*pb.SLOViolationsResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	
	violations := s.store.GetSLOViolations() // Get all violations
	
	var pbViolations []*pb.SLOViolation
	for i, v := range violations {
		if i >= limit {
			break
		}
		
		pbViolations = append(pbViolations, &pb.SLOViolation{
			Namespace:  v.Namespace,
			Table:      v.Table,
			TargetMs:   v.SLOTarget,
			CurrentP99: v.ActualFreshness,
			Severity:   parseSeverityToFloat(v.Severity),
			DetectedAt: timestamppb.New(v.ViolationStart),
		})
	}
	
	return &pb.SLOViolationsResponse{
		Violations: pbViolations,
	}, nil
}

// broadcastUpdate sends an update to all streaming clients
func (s *Server) broadcastUpdate(update *pb.WatermarkUpdate) {
	s.streamMu.RLock()
	defer s.streamMu.RUnlock()
	
	for _, ch := range s.streamClients {
		select {
		case ch <- update:
		default:
			// Channel full, skip
		}
	}
}

// calculatePercentiles calculates p50, p95, p99 from a slice of values
func calculatePercentiles(values []int64) (p50, p95, p99 int64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	
	// Simple percentile calculation (in production, use a proper algorithm)
	// For now, just return approximations
	sum := int64(0)
	max := int64(0)
	for _, v := range values {
		sum += v
		if v > max {
			max = v
		}
	}
	
	avg := sum / int64(len(values))
	p50 = avg
	p95 = avg + (max-avg)/2
	p99 = max
	
	return p50, p95, p99
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// parseSeverityToFloat converts severity string to float32 for proto
func parseSeverityToFloat(severity string) float32 {
	switch severity {
	case "critical":
		return 2.0
	case "warning":
		return 1.0
	default:
		return 0.0
	}
}