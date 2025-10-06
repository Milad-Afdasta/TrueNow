package writer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/Milad-Afdasta/TrueNow/shared/proto/pb/hottier"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
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

	copyEndpoints := make([]string, len(endpoints))
	copy(copyEndpoints, endpoints)

	htw := &HotTierWriter{
		endpoints: copyEndpoints,
		clients:   make([]pb.HotTierClient, len(copyEndpoints)),
		conns:     make([]*grpc.ClientConn, len(copyEndpoints)),
	}

	for i := range copyEndpoints {
		if err := htw.reconnect(i); err != nil {
			log.WithError(err).Warnf("hot-tier writer: initial connect failed for %s", copyEndpoints[i])
		}
	}

	log.Infof("Hot-tier writer configured with %d endpoints", len(copyEndpoints))
	return htw
}

// connect establishes gRPC connection with retry
func (htw *HotTierWriter) connect(endpoint string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(100*1024*1024),
			grpc.MaxCallSendMsgSize(100*1024*1024),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  100 * time.Millisecond,
				Multiplier: 2.0,
				MaxDelay:   10 * time.Second,
			},
		}),
	}

	return grpc.DialContext(ctx, endpoint, opts...)
}

func (htw *HotTierWriter) reconnect(idx int) error {
	if idx < 0 || idx >= len(htw.endpoints) {
		return errors.New("hot-tier writer: endpoint index out of range")
	}

	endpoint := htw.endpoints[idx]
	conn, err := htw.connect(endpoint)
	if err != nil {
		return err
	}

	htw.mu.Lock()
	defer htw.mu.Unlock()

	if existing := htw.conns[idx]; existing != nil {
		_ = existing.Close()
	}

	htw.conns[idx] = conn
	htw.clients[idx] = pb.NewHotTierClient(conn)
	log.Infof("hot-tier writer connected to %s", endpoint)
	return nil
}

func (htw *HotTierWriter) selectClient() (pb.HotTierClient, int, string, error) {
	if len(htw.endpoints) == 0 {
		return nil, -1, "", errors.New("hot-tier writer: no endpoints configured")
	}

	for attempts := 0; attempts < len(htw.endpoints); attempts++ {
		idx := int(htw.current.Add(1) % uint64(len(htw.endpoints)))

		htw.mu.RLock()
		client := htw.clients[idx]
		endpoint := htw.endpoints[idx]
		htw.mu.RUnlock()

		if client != nil {
			return client, idx, endpoint, nil
		}

		if err := htw.reconnect(idx); err != nil {
			log.WithError(err).Warnf("hot-tier writer: reconnect failed for %s", endpoint)
			continue
		}

		htw.mu.RLock()
		client = htw.clients[idx]
		endpoint = htw.endpoints[idx]
		htw.mu.RUnlock()
		if client != nil {
			return client, idx, endpoint, nil
		}
	}

	return nil, -1, "", errors.New("hot-tier writer: no available clients")
}

func (htw *HotTierWriter) invalidateClient(idx int) {
	htw.mu.Lock()
	defer htw.mu.Unlock()
	if idx < 0 || idx >= len(htw.conns) {
		return
	}
	if htw.conns[idx] != nil {
		_ = htw.conns[idx].Close()
	}
	htw.conns[idx] = nil
	htw.clients[idx] = nil
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

	client, idx, endpoint, err := htw.selectClient()
	if err != nil {
		htw.errors.Add(uint64(len(events)))
		return err
	}

	// Send with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := client.ApplyBatch(ctx, req)
	if err != nil {
		htw.errors.Add(uint64(len(events)))
		htw.invalidateClient(idx)
		return fmt.Errorf("hot-tier writer: apply batch failed on %s: %w", endpoint, err)
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

	for i, conn := range htw.conns {
		if conn != nil {
			_ = conn.Close()
		}
		htw.conns[i] = nil
		htw.clients[i] = nil
	}

	log.Info("Hot-tier writer closed")
}

// GetStats returns writer statistics
func (htw *HotTierWriter) GetStats() map[string]uint64 {
	htw.mu.RLock()
	defer htw.mu.RUnlock()

	active := uint64(0)
	for _, client := range htw.clients {
		if client != nil {
			active++
		}
	}

	return map[string]uint64{
		"written":        htw.written.Load(),
		"errors":         htw.errors.Load(),
		"latency_us":     htw.latency.Load(),
		"active_clients": active,
	}
}
