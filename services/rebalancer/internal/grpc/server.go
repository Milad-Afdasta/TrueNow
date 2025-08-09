package grpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/Milad-Afdasta/TrueNow/proto/rebalancer"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/coordinator"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/internal/detector"
	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/pkg/types"
)

// Server implements the gRPC RebalancerService
type Server struct {
	pb.UnimplementedRebalancerServiceServer
	
	detector    *detector.HotspotDetector
	coordinator *coordinator.ShardCoordinator
	config      *types.RebalancerConfig
	port        int
	
	// For streaming
	streamClients map[string]chan *pb.ShardUpdate
	streamMu      sync.RWMutex
}

// NewServer creates a new gRPC server
func NewServer(
	detector *detector.HotspotDetector,
	coordinator *coordinator.ShardCoordinator,
	config *types.RebalancerConfig,
	port int,
) *Server {
	return &Server{
		detector:      detector,
		coordinator:   coordinator,
		config:        config,
		port:          port,
		streamClients: make(map[string]chan *pb.ShardUpdate),
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	
	grpcServer := grpc.NewServer()
	pb.RegisterRebalancerServiceServer(grpcServer, s)
	
	// Register reflection service for grpcurl
	reflection.Register(grpcServer)
	
	return grpcServer.Serve(lis)
}

// GetShardAssignments returns current shard assignments
func (s *Server) GetShardAssignments(ctx context.Context, req *pb.GetShardAssignmentsRequest) (*pb.ShardAssignmentsResponse, error) {
	epoch := s.coordinator.GetCurrentEpoch()
	
	assignments := make(map[string]*pb.ShardAssignment)
	for shardID, assignment := range epoch.Assignments {
		pbAssignment := &pb.ShardAssignment{
			ShardId:       fmt.Sprintf("shard-%d", shardID),
			PrimaryOwner:  fmt.Sprintf("node-%d", shardID%10), // Simplified
			VirtualShards: int32(len(assignment.VirtualShards)),
			LoadScore:     0.0, // Will be filled from metrics
			State:         convertShardState(assignment.State),
			Metadata:      make(map[string]string),
		}
		
		// Get metrics for this shard
		if metrics := s.detector.GetShardMetrics(shardID); metrics != nil {
			pbAssignment.LoadScore = metrics.RequestsPerSecond / 1000.0 // Normalize
		}
		
		assignments[pbAssignment.ShardId] = pbAssignment
	}
	
	return &pb.ShardAssignmentsResponse{
		Assignments:  assignments,
		CurrentEpoch: epoch.Epoch,
		LastUpdate:   timestamppb.Now(),
	}, nil
}

// StreamShardUpdates streams real-time shard updates
func (s *Server) StreamShardUpdates(req *pb.StreamShardUpdatesRequest, stream pb.RebalancerService_StreamShardUpdatesServer) error {
	// Create channel for this client
	clientID := fmt.Sprintf("%d", time.Now().UnixNano())
	updateChan := make(chan *pb.ShardUpdate, 100)
	
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
			if err := stream.Send(update); err != nil {
				return err
			}
			
		case <-ticker.C:
			// Send periodic status updates
			dist := s.detector.AnalyzeDistribution()
			
			// Send load alerts for hot shards
			for _, hotShard := range dist.HotShards {
				if metrics := s.detector.GetShardMetrics(hotShard); metrics != nil {
					alert := &pb.LoadAlert{
						NodeId:      fmt.Sprintf("node-%d", hotShard%10),
						Type:        pb.AlertType_HOTSPOT_DETECTED,
						CurrentLoad: metrics.RequestsPerSecond,
						Threshold:   s.config.HotShardThreshold * dist.AverageLoad,
						Message:     fmt.Sprintf("Shard %d is experiencing high load", hotShard),
					}
					
					update := &pb.ShardUpdate{
						Update: &pb.ShardUpdate_LoadAlert{
							LoadAlert: alert,
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

// GetLoadDistribution returns load distribution metrics
func (s *Server) GetLoadDistribution(ctx context.Context, req *pb.GetLoadDistributionRequest) (*pb.LoadDistributionResponse, error) {
	dist := s.detector.AnalyzeDistribution()
	
	// Build node loads map
	nodeLoads := make(map[string]*pb.NodeLoad)
	epoch := s.coordinator.GetCurrentEpoch()
	
	// Group shards by node (simplified - assumes 10 nodes)
	for shardID, assignment := range epoch.Assignments {
		nodeID := fmt.Sprintf("node-%d", shardID%10)
		
		if nodeLoads[nodeID] == nil {
			nodeLoads[nodeID] = &pb.NodeLoad{
				NodeId:             nodeID,
				ShardCount:         0,
				VirtualShardCount:  0,
				RequestRate:        0,
			}
		}
		
		nodeLoads[nodeID].ShardCount++
		nodeLoads[nodeID].VirtualShardCount += int32(len(assignment.VirtualShards))
		
		// Add metrics
		if metrics := s.detector.GetShardMetrics(shardID); metrics != nil {
			nodeLoads[nodeID].RequestRate += metrics.RequestsPerSecond
			nodeLoads[nodeID].CpuUsage = min(100, nodeLoads[nodeID].RequestRate/100) // Simplified
			nodeLoads[nodeID].MemoryUsage = min(100, float64(nodeLoads[nodeID].ShardCount)*10) // Simplified
			
			// Add hot keys
			for _, hotKey := range metrics.HotKeys {
				nodeLoads[nodeID].HotKeys = append(nodeLoads[nodeID].HotKeys, hotKey.Key)
			}
		}
	}
	
	// Calculate percentiles (simplified)
	var loads []float64
	for _, node := range nodeLoads {
		loads = append(loads, node.RequestRate)
	}
	
	p50, p95, p99 := calculatePercentiles(loads)
	
	return &pb.LoadDistributionResponse{
		NodeLoads:          nodeLoads,
		GiniCoefficient:    dist.GiniCoefficient,
		P50Load:            p50,
		P95Load:            p95,
		P99Load:            p99,
		TotalShards:        int32(len(epoch.Assignments)),
		TotalVirtualShards: int32(len(epoch.VirtualToPhysical)),
		CalculatedAt:       timestamppb.Now(),
	}, nil
}

// BatchReportLoad handles batch load reports
func (s *Server) BatchReportLoad(ctx context.Context, req *pb.BatchReportLoadRequest) (*pb.BatchReportLoadResponse, error) {
	accepted := 0
	rejected := 0
	var errors []string
	
	for _, report := range req.Reports {
		// Parse shard ID
		var shardID int32
		if _, err := fmt.Sscanf(report.ShardId, "shard-%d", &shardID); err != nil {
			rejected++
			errors = append(errors, fmt.Sprintf("invalid shard ID: %s", report.ShardId))
			continue
		}
		
		// Update metrics
		metrics := &types.ShardMetrics{
			ShardID:           shardID,
			RequestsPerSecond: report.RequestRate,
			BytesPerSecond:    report.RequestRate * 1024, // Assume 1KB per request
			LastUpdate:        report.Timestamp.AsTime(),
		}
		
		// Process hot keys
		for key, freq := range report.KeyFrequencies {
			if float64(freq) > report.RequestRate*0.1 { // More than 10% of traffic
				metrics.HotKeys = append(metrics.HotKeys, types.HotKey{
					Key:               key,
					RequestsPerSecond: float64(freq),
					PercentOfShard:    float64(freq) / report.RequestRate,
				})
			}
		}
		
		s.detector.UpdateMetrics(shardID, metrics)
		accepted++
	}
	
	return &pb.BatchReportLoadResponse{
		Accepted: int32(accepted),
		Rejected: int32(rejected),
		Errors:   errors,
	}, nil
}

// GetHotspots returns detected hotspots
func (s *Server) GetHotspots(ctx context.Context, req *pb.GetHotspotsRequest) (*pb.HotspotsResponse, error) {
	dist := s.detector.AnalyzeDistribution()
	
	var hotspots []*pb.Hotspot
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}
	
	for i, shardID := range dist.HotShards {
		if i >= limit {
			break
		}
		
		metrics := s.detector.GetShardMetrics(shardID)
		if metrics == nil {
			continue
		}
		
		hotspot := &pb.Hotspot{
			NodeId:            fmt.Sprintf("node-%d", shardID%10),
			ShardId:           fmt.Sprintf("shard-%d", shardID),
			LoadScore:         metrics.RequestsPerSecond,
			DeviationFromMean: (metrics.RequestsPerSecond - dist.AverageLoad) / dist.StdDeviation,
		}
		
		// Add hot keys
		for _, hotKey := range metrics.HotKeys {
			hotspot.HotKeys = append(hotspot.HotKeys, hotKey.Key)
		}
		
		// Suggest mitigation strategy
		if len(metrics.HotKeys) > 0 && metrics.HotKeys[0].PercentOfShard > 0.5 {
			hotspot.SuggestedStrategy = pb.MitigationStrategy_CACHE_HOT_KEYS
		} else if metrics.RequestsPerSecond > dist.AverageLoad*3 {
			hotspot.SuggestedStrategy = pb.MitigationStrategy_SPLIT_SHARD
		} else {
			hotspot.SuggestedStrategy = pb.MitigationStrategy_MIGRATE_SHARD
		}
		
		hotspots = append(hotspots, hotspot)
	}
	
	return &pb.HotspotsResponse{
		Hotspots:   hotspots,
		AvgLoad:    dist.AverageLoad,
		DetectedAt: timestamppb.Now(),
	}, nil
}

// TriggerRebalance manually triggers rebalancing
func (s *Server) TriggerRebalance(ctx context.Context, req *pb.TriggerRebalanceRequest) (*pb.TriggerRebalanceResponse, error) {
	// In production, this would trigger actual rebalancing
	// For now, simulate the response
	
	dist := s.detector.AnalyzeDistribution()
	shardsToMove := len(dist.HotShards) / 2 // Simplified
	
	return &pb.TriggerRebalanceResponse{
		Success:                  true,
		RebalanceId:              fmt.Sprintf("rb-%d", time.Now().Unix()),
		ShardsToMove:             int32(shardsToMove),
		EstimatedDurationSeconds: float64(shardsToMove * 10), // 10 seconds per shard
		Message:                  fmt.Sprintf("Rebalancing %d shards to improve distribution", shardsToMove),
	}, nil
}

// GetRebalanceHistory returns rebalancing history
func (s *Server) GetRebalanceHistory(ctx context.Context, req *pb.GetRebalanceHistoryRequest) (*pb.RebalanceHistoryResponse, error) {
	// In production, this would return actual history
	// For now, return empty
	return &pb.RebalanceHistoryResponse{
		Events: []*pb.RebalanceEvent{},
	}, nil
}

// Helper functions

func convertShardState(state types.ShardState) pb.ShardState {
	switch state {
	case types.ShardStateActive:
		return pb.ShardState_ACTIVE
	case types.ShardStateMigrating:
		return pb.ShardState_MIGRATING
	case types.ShardStateSplitting:
		return pb.ShardState_SPLITTING
	case types.ShardStateMerging:
		return pb.ShardState_MERGING
	default:
		return pb.ShardState_INACTIVE
	}
}

func calculatePercentiles(values []float64) (p50, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	
	// Simple percentile calculation
	sum := 0.0
	max := 0.0
	for _, v := range values {
		sum += v
		if v > max {
			max = v
		}
	}
	
	avg := sum / float64(len(values))
	p50 = avg
	p95 = avg + (max-avg)*0.75
	p99 = max
	
	return p50, p95, p99
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}