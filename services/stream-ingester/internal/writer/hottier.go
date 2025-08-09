package writer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/Milad-Afdasta/TrueNow/shared/proto/pb/hottier"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/keepalive"
)

// HotTierWriter writes batches to hot-tier nodes
type HotTierWriter struct {
	endpoints []string
	clients   []pb.HotTierClient
	conns     []*grpc.ClientConn
	
	// Round-robin selection
	current atomic.Uint64
	
	// Stats
	written atomic.Uint64
	errors  atomic.Uint64
	latency atomic.Uint64 // microseconds
	
	mu sync.RWMutex
}

// NewHotTierWriter creates a new hot-tier writer
func NewHotTierWriter(endpoints ...string) *HotTierWriter {
	if len(endpoints) == 0 {
		endpoints = []string{"localhost:9090"}
	}
	
	htw := &HotTierWriter{
		endpoints: endpoints,
		clients:   make([]pb.HotTierClient, len(endpoints)),
		conns:     make([]*grpc.ClientConn, len(endpoints)),
	}
	
	// Connect to all endpoints
	for i, endpoint := range endpoints {
		conn, err := htw.connect(endpoint)
		if err != nil {
			log.Errorf("Failed to connect to hot-tier %s: %v", endpoint, err)
			continue
		}
		
		htw.conns[i] = conn
		htw.clients[i] = pb.NewHotTierClient(conn)
	}
	
	log.Infof("Hot-tier writer connected to %d endpoints", len(endpoints))
	
	return htw
}

// connect establishes gRPC connection with retry
func (htw *HotTierWriter) connect(endpoint string) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithTimeout(10 * time.Second),
		
		// Connection pooling
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(100 * 1024 * 1024), // 100MB
			grpc.MaxCallSendMsgSize(100 * 1024 * 1024),
		),
		
		// Keepalive
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		
		// Retry with backoff
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  100 * time.Millisecond,
				Multiplier: 2.0,
				MaxDelay:   10 * time.Second,
			},
		}),
	}
	
	return grpc.Dial(endpoint, opts...)
}

// WriteBatch writes a batch to hot-tier
func (htw *HotTierWriter) WriteBatch(events []interface{}) error {
	if len(events) == 0 {
		return nil
	}
	
	// Convert to protobuf records
	records := make([]*pb.Record, 0, len(events))
	var namespace, table string
	
	for _, e := range events {
		// Type assert to get the actual event
		event, ok := e.(map[string]interface{})
		if !ok {
			log.Warnf("Invalid event type: %T", e)
			continue
		}
		
		// Extract fields from the event
		eventID, _ := event["EventID"].(string)
		eventTime, _ := event["EventTime"].(int64)
		ns, _ := event["Namespace"].(string)
		tbl, _ := event["Table"].(string)
		data, _ := event["Data"].(map[string]interface{})
		
		// Set namespace and table from first event
		if namespace == "" && ns != "" {
			namespace = ns
			table = tbl
		}
		
		// Create group key from dimensions
		groupKey := "default"
		if data != nil {
			// Use first dimension as group key for now
			for k, v := range data {
				groupKey = fmt.Sprintf("%s:%v", k, v)
				break
			}
		}
		
		record := &pb.Record{
			EventId:     eventID,
			EventTimeUs: eventTime, // Already in microseconds
			GroupKey:    groupKey,
			Metrics:     []float64{1}, // Count metric for now
			Revision:    1,
		}
		records = append(records, record)
	}
	
	// Create request
	req := &pb.ApplyBatchRequest{
		Records:   records,
		Epoch:     1,
		Namespace: namespace,
		Table:     table,
	}
	
	// Select hot-tier node (round-robin)
	idx := htw.current.Add(1) % uint64(len(htw.clients))
	client := htw.clients[idx]
	
	if client == nil {
		htw.errors.Add(1)
		log.Error("No available hot-tier client")
		return fmt.Errorf("no available hot-tier client")
	}
	
	// Send with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	start := time.Now()
	resp, err := client.ApplyBatch(ctx, req)
	if err != nil {
		htw.errors.Add(1)
		return err
	}
	
	// Update stats
	htw.written.Add(uint64(resp.AppliedCount))
	htw.latency.Store(uint64(time.Since(start).Microseconds()))
	
	log.Debugf("Wrote batch to hot-tier: applied=%d, deduped=%d, watermark=%d",
		resp.AppliedCount, resp.DedupedCount, resp.WatermarkUs)
	
	return nil
}

// Close closes all connections
func (htw *HotTierWriter) Close() {
	htw.mu.Lock()
	defer htw.mu.Unlock()
	
	for _, conn := range htw.conns {
		if conn != nil {
			conn.Close()
		}
	}
	
	log.Info("Hot-tier writer closed")
}

// GetStats returns writer statistics
func (htw *HotTierWriter) GetStats() map[string]uint64 {
	return map[string]uint64{
		"written":         htw.written.Load(),
		"errors":          htw.errors.Load(),
		"latency_us":      htw.latency.Load(),
		"active_clients":  uint64(len(htw.clients)),
	}
}