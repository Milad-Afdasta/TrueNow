package producer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/segmentio/kafka-go"
	log "github.com/sirupsen/logrus"
)

// BatchProducer implements high-throughput batched Kafka producer
// Optimized for 1M+ events/sec per instance
type BatchProducer struct {
	writers   []*kafka.Writer
	numShards int
	
	// Batching
	batches    []chan *Message
	flushChan  chan int
	
	// Stats
	sent       atomic.Uint64
	errors     atomic.Uint64
	bytesOut   atomic.Uint64
	
	// Control
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// Message represents a Kafka message
type Message struct {
	Topic     string
	Key       []byte
	Value     []byte
	Headers   []kafka.Header
	Partition int32
	Offset    int64 // Set after send
}

// NewBatchProducer creates a new high-performance batch producer
func NewBatchProducer(brokers []string) *BatchProducer {
	// TEST MODE: If no brokers, run in test mode
	if len(brokers) == 0 {
		log.Warn("Running in TEST MODE - events will be logged, not sent to Kafka")
		return &BatchProducer{
			writers:   nil,
			numShards: 1,
			batches:   make([]chan *Message, 1),
			flushChan: make(chan int, 1),
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	
	// Use multiple shards for parallelism (avoid lock contention)
	numShards := 16 // Optimal for most systems
	
	bp := &BatchProducer{
		writers:   make([]*kafka.Writer, numShards),
		numShards: numShards,
		batches:   make([]chan *Message, numShards),
		flushChan: make(chan int, numShards),
		ctx:       ctx,
		cancel:    cancel,
	}
	
	// Create sharded writers for parallel production
	for i := 0; i < numShards; i++ {
		bp.writers[i] = &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Balancer:              &kafka.Hash{}, // Hash balancer for consistent partitioning
			BatchSize:             1000,          // Messages per batch
			BatchBytes:            1048576,       // 1MB max batch size
			BatchTimeout:          10 * time.Millisecond,
			WriteTimeout:          10 * time.Second,
			RequiredAcks:          kafka.RequireOne, // For ultra-low latency (change to RequireAll for durability)
			Compression:           kafka.Zstd, // Fast compression
			Async:                 false, // Synchronous for idempotency
			AllowAutoTopicCreation: false,
			
			// Performance tuning
			MaxAttempts:            3,
			WriteBackoffMin:        10 * time.Millisecond,
			WriteBackoffMax:        1 * time.Second,
			
			// Transport optimizations
			Transport: &kafka.Transport{
				DialTimeout:   10 * time.Second,
				IdleTimeout:   30 * time.Second,
				MetadataTTL:   60 * time.Second,
				ClientID:      "flow-gateway",
				
				// TLS would go here in production
				// TLS: &tls.Config{},
			},
		}
		
		// Create batch channel with buffer
		bp.batches[i] = make(chan *Message, 10000)
		
		// Start batch worker
		bp.wg.Add(1)
		go bp.batchWorker(i)
	}
	
	// Start flush ticker
	bp.wg.Add(1)
	go bp.flushTicker()
	
	log.Infof("Batch producer initialized with %d shards", numShards)
	
	return bp
}

// SendBatch sends a batch of events to Kafka
func (bp *BatchProducer) SendBatch(events []*Event) ([]int64, error) {
	if len(events) == 0 {
		return nil, nil
	}
	
	// TEST MODE: Just log events
	if bp.writers == nil {
		log.Infof("TEST MODE: Would send %d events to Kafka", len(events))
		for i, event := range events {
			log.Debugf("Event %d: ID=%s, Namespace=%s, Table=%s", 
				i, event.EventID, event.Namespace, event.Table)
		}
		// Return mock offsets
		offsets := make([]int64, len(events))
		for i := range offsets {
			offsets[i] = int64(i)
		}
		bp.sent.Add(uint64(len(events)))
		return offsets, nil
	}
	
	offsets := make([]int64, len(events))
	messages := make([]kafka.Message, 0, len(events))
	
	// Convert events to Kafka messages
	for _, event := range events {
		// Serialize event (in production, use protobuf or flatbuffers)
		value := bp.serializeEvent(event)
		
		// Create message with headers for metadata
		msg := kafka.Message{
			Topic:     bp.getTopicForEvent(event),
			Key:       []byte(event.EventID),
			Value:     value,
			Partition: int(event.Partition),
			Headers: []kafka.Header{
				{Key: "namespace", Value: s2b(event.Namespace)},
				{Key: "table", Value: s2b(event.Table)},
				{Key: "epoch", Value: i64tob(event.Epoch)},
			},
		}
		
		messages = append(messages, msg)
	}
	
	// Select shard based on first event (could use round-robin)
	shardIdx := events[0].HashKey % uint64(bp.numShards)
	writer := bp.writers[shardIdx]
	
	// Send batch with retries
	err := bp.sendWithRetry(writer, messages)
	if err != nil {
		bp.errors.Add(uint64(len(messages)))
		return nil, err
	}
	
	// Track stats
	bp.sent.Add(uint64(len(messages)))
	for i, msg := range messages {
		offsets[i] = msg.Offset
		bp.bytesOut.Add(uint64(len(msg.Value)))
	}
	
	return offsets, nil
}

// sendWithRetry sends messages with exponential backoff retry
func (bp *BatchProducer) sendWithRetry(writer *kafka.Writer, messages []kafka.Message) error {
	var err error
	maxRetries := 3
	backoff := 10 * time.Millisecond
	
	for i := 0; i < maxRetries; i++ {
		err = writer.WriteMessages(bp.ctx, messages...)
		if err == nil {
			return nil
		}
		
		// Check if error is retryable
		if !isRetryableKafkaError(err) {
			return err
		}
		
		if i < maxRetries-1 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	
	return err
}

// batchWorker processes messages in batches
func (bp *BatchProducer) batchWorker(shardIdx int) {
	defer bp.wg.Done()
	
	writer := bp.writers[shardIdx]
	batchChan := bp.batches[shardIdx]
	
	// Local batch buffer
	batch := make([]kafka.Message, 0, 1000)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-bp.ctx.Done():
			// Flush remaining
			if len(batch) > 0 {
				writer.WriteMessages(context.Background(), batch...)
			}
			return
			
		case msg := <-batchChan:
			// Convert internal message to Kafka message
			batch = append(batch, kafka.Message{
				Topic:     msg.Topic,
				Key:       msg.Key,
				Value:     msg.Value,
				Headers:   msg.Headers,
				Partition: int(msg.Partition),
			})
			
			// Flush if batch is full
			if len(batch) >= 1000 {
				if err := writer.WriteMessages(bp.ctx, batch...); err != nil {
					bp.errors.Add(uint64(len(batch)))
					log.Errorf("Batch write failed: %v", err)
				} else {
					bp.sent.Add(uint64(len(batch)))
				}
				batch = batch[:0] // Reset batch
			}
			
		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				if err := writer.WriteMessages(bp.ctx, batch...); err != nil {
					bp.errors.Add(uint64(len(batch)))
					log.Errorf("Batch write failed: %v", err)
				} else {
					bp.sent.Add(uint64(len(batch)))
				}
				batch = batch[:0]
			}
			
		case <-bp.flushChan:
			// Force flush
			if len(batch) > 0 {
				writer.WriteMessages(bp.ctx, batch...)
				batch = batch[:0]
			}
		}
	}
}

// flushTicker periodically triggers flushes
func (bp *BatchProducer) flushTicker() {
	defer bp.wg.Done()
	
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-bp.ctx.Done():
			return
		case <-ticker.C:
			// Trigger flush on all shards
			for i := 0; i < bp.numShards; i++ {
				select {
				case bp.flushChan <- i:
				default:
					// Channel full, skip
				}
			}
		}
	}
}

// serializeEvent serializes an event to bytes
// In production, use protobuf or flatbuffers for zero-copy serialization
func (bp *BatchProducer) serializeEvent(event *Event) []byte {
	// Simplified JSON serialization
	// Properly marshal dims and metrics to JSON
	dimsJSON, _ := json.Marshal(event.Dims)
	metricsJSON, _ := json.Marshal(event.Metrics)
	return []byte(fmt.Sprintf(`{"id":"%s","time":%d,"dims":%s,"metrics":%s}`,
		event.EventID, event.EventTime, dimsJSON, metricsJSON))
}

// getTopicForEvent determines the Kafka topic for an event
func (bp *BatchProducer) getTopicForEvent(event *Event) string {
	// For now, use the simple "events" topic
	// In production: fmt.Sprintf("stream.%s.%s.%d", event.Namespace, event.Table, event.TableVersion)
	return "events"
}

// Close gracefully shuts down the producer
func (bp *BatchProducer) Close() error {
	bp.cancel()
	
	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		bp.wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// Clean shutdown
	case <-time.After(5 * time.Second):
		log.Warn("Producer shutdown timeout")
	}
	
	// Close all writers
	for _, writer := range bp.writers {
		if err := writer.Close(); err != nil {
			log.Errorf("Failed to close writer: %v", err)
		}
	}
	
	log.Infof("Producer stats - Sent: %d, Errors: %d, Bytes: %d",
		bp.sent.Load(), bp.errors.Load(), bp.bytesOut.Load())
	
	return nil
}

// GetStats returns producer statistics
func (bp *BatchProducer) GetStats() map[string]uint64 {
	return map[string]uint64{
		"sent":      bp.sent.Load(),
		"errors":    bp.errors.Load(),
		"bytes_out": bp.bytesOut.Load(),
	}
}

// Event type (shared with ingestion)
type Event struct {
	EventID      string
	EventTime    int64
	Namespace    string
	Table        string
	TableVersion int
	Epoch        int64
	Revision     int
	Dims         map[string]string
	Metrics      map[string]float64
	HashKey      uint64
	Partition    int32
}

// Helper functions

func isRetryableKafkaError(err error) bool {
	// Check for retryable Kafka errors
	errStr := err.Error()
	return errStr == "context deadline exceeded" ||
		errStr == "connection refused" ||
		errStr == "broken pipe"
}

func s2b(s string) []byte {
	return *(*[]byte)(unsafe.Pointer(&s))
}

func i64tob(i int64) []byte {
	b := make([]byte, 8)
	*(*int64)(unsafe.Pointer(&b[0])) = i
	return b
}