package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/stream-ingester/internal/consumer"
	"github.com/Milad-Afdasta/TrueNow/services/stream-ingester/internal/processor"
	"github.com/Milad-Afdasta/TrueNow/services/stream-ingester/internal/writer"
	"github.com/segmentio/kafka-go"
	log "github.com/sirupsen/logrus"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.DebugLevel)

	// Use all CPU cores
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Configuration
	config := &Config{
		KafkaBrokers:    getEnvOrDefault("KAFKA_BROKERS", "localhost:19092"),
		ConsumerGroup:   getEnvOrDefault("CONSUMER_GROUP", "stream-ingester"),
		Topics:          []string{"events"},
		HotTierEndpoint: getEnvOrDefault("HOT_TIER_ENDPOINT", "localhost:9090"),
		NumWorkers:      10,
		BatchSize:       1000,
		BatchTimeout:    100 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create components
	kafkaConsumer := consumer.NewKafkaConsumer(config.KafkaBrokers, config.ConsumerGroup, config.Topics)
	eventProcessor := processor.NewEventProcessor(config.BatchSize)
	hotTierEndpoints := splitAndTrim(config.HotTierEndpoint)
	hotTierWriter := writer.NewHotTierWriter(hotTierEndpoints...)

	// Start workers
	var wg sync.WaitGroup
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}
	flushInterval := config.BatchTimeout
	if flushInterval <= 0 {
		flushInterval = 100 * time.Millisecond
	}

	for i := 0; i < config.NumWorkers; i++ {
		wg.Add(1)
		go worker(ctx, &wg, kafkaConsumer, eventProcessor, hotTierWriter, i, batchSize, flushInterval)
	}

	log.Infof("Stream Ingester started with %d workers", config.NumWorkers)

	// Wait for interrupt
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down stream ingester...")
	cancel()

	// Wait for workers to finish
	wg.Wait()

	// Close connections
	kafkaConsumer.Close()
	hotTierWriter.Close()

	log.Info("Stream ingester exited")
}

func worker(ctx context.Context, wg *sync.WaitGroup,
	cons *consumer.KafkaConsumer,
	processor *processor.EventProcessor,
	writer *writer.HotTierWriter,
	workerID int,
	batchSize int,
	flushInterval time.Duration) {

	defer wg.Done()

	if batchSize <= 0 {
		batchSize = 1
	}
	if flushInterval <= 0 {
		flushInterval = 100 * time.Millisecond
	}

	batch := make([]*inFlightEvent, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func(current []*inFlightEvent) ([]*inFlightEvent, error) {
		return flushBatch(ctx, current, processor, writer, workerID)
	}

	for {
		select {
		case <-ctx.Done():
			if len(batch) > 0 {
				if updated, err := flush(batch); err != nil {
					log.WithError(err).Warnf("Worker %d: graceful flush aborted", workerID)
				} else {
					batch = updated
				}
			}
			return

		case record, ok := <-cons.Messages():
			if !ok {
				if len(batch) > 0 {
					if updated, err := flush(batch); err != nil {
						log.WithError(err).Warnf("Worker %d: final flush aborted", workerID)
					} else {
						batch = updated
					}
				}
				return
			}

			event := parseKafkaMessage(record.Message)
			if event == nil {
				ackWithTimeout(record, 5*time.Second)
				continue
			}

			batch = append(batch, &inFlightEvent{event: event, record: record})

			if len(batch) >= batchSize {
				updated, err := flush(batch)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.WithError(err).Errorf("Worker %d: retrying batch of %d events", workerID, len(batch))
					continue
				}
				batch = updated
			}

		case <-ticker.C:
			if len(batch) == 0 {
				continue
			}
			updated, err := flush(batch)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.WithError(err).Errorf("Worker %d: retrying batch of %d events", workerID, len(batch))
				continue
			}
			batch = updated
		}
	}
}

func processBatch(ctx context.Context, batch []*inFlightEvent,
	processor *processor.EventProcessor,
	writer *writer.HotTierWriter,
	workerID int) error {

	if len(batch) == 0 {
		return nil
	}

	start := time.Now()

	events := make([]interface{}, len(batch))
	for i, item := range batch {
		events[i] = map[string]interface{}{
			"EventID":   item.event.EventID,
			"EventTime": item.event.EventTime,
			"Namespace": item.event.Namespace,
			"Table":     item.event.Table,
			"Data":      item.event.Data,
		}
	}

	processed := processor.ProcessBatch(events)
	if len(processed) == 0 {
		ackBatch(batch, 5*time.Second)
		return nil
	}

	if err := writer.WriteBatch(processed); err != nil {
		return err
	}

	ackBatch(batch, 5*time.Second)
	log.Debugf("Worker %d: processed %d events in %s", workerID, len(batch), time.Since(start))
	return nil
}

func flushBatch(ctx context.Context, batch []*inFlightEvent,
	processor *processor.EventProcessor,
	writer *writer.HotTierWriter,
	workerID int) ([]*inFlightEvent, error) {

	if len(batch) == 0 {
		return batch[:0], nil
	}

	backoff := 200 * time.Millisecond
	for {
		if err := processBatch(ctx, batch, processor, writer, workerID); err != nil {
			select {
			case <-ctx.Done():
				return batch, ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < 5*time.Second {
				backoff *= 2
				if backoff > 5*time.Second {
					backoff = 5 * time.Second
				}
			}
			continue
		}
		return batch[:0], nil
	}
}

func ackBatch(batch []*inFlightEvent, timeout time.Duration) {
	for _, item := range batch {
		ackWithTimeout(item.record, timeout)
	}
}

func ackWithTimeout(record *consumer.Record, timeout time.Duration) {
	if record == nil {
		return
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if err := record.Ack(ctx); err != nil {
		log.WithError(err).Warn("stream-ingester: failed to commit kafka offset")
	}
}

func splitAndTrim(list string) []string {
	if list == "" {
		return nil
	}
	parts := strings.Split(list, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

type Config struct {
	KafkaBrokers    string
	ConsumerGroup   string
	Topics          []string
	HotTierEndpoint string
	NumWorkers      int
	BatchSize       int
	BatchTimeout    time.Duration
}

type Event struct {
	EventID   string
	EventTime int64
	Namespace string
	Table     string
	Data      map[string]interface{}
}

type inFlightEvent struct {
	event  *Event
	record *consumer.Record
}

func parseKafkaMessage(msg kafka.Message) *Event {
	// Parse the event from Kafka message
	var eventData map[string]interface{}
	if err := json.Unmarshal(msg.Value, &eventData); err != nil {
		log.Warnf("Failed to parse event JSON: %v", err)
		return nil
	}

	// Extract namespace and table from headers
	var namespace, table string
	for _, header := range msg.Headers {
		switch header.Key {
		case "namespace":
			namespace = string(header.Value)
		case "table":
			table = string(header.Value)
		}
	}
	if namespace == "" {
		if ns, ok := eventData["namespace"].(string); ok {
			namespace = ns
		}
	}
	if table == "" {
		if tbl, ok := eventData["table"].(string); ok {
			table = tbl
		}
	}

	// Extract event fields
	eventID, _ := eventData["id"].(string)
	if eventID == "" {
		if len(msg.Key) > 0 {
			eventID = fmt.Sprintf("%s-%d", string(msg.Key), msg.Offset)
		} else {
			eventID = fmt.Sprintf("event-%d", time.Now().UnixNano())
		}
	}

	eventTimeMicros := extractEventTime(eventData)

	// Extract dimensions
	dims, _ := eventData["dims"].(map[string]interface{})
	if dims == nil {
		dims = make(map[string]interface{})
	}
	if extraDims, ok := eventData["dimensions"].(map[string]interface{}); ok {
		for k, v := range extraDims {
			dims[k] = v
		}
	}

	if namespace == "" || table == "" {
		log.Warnf("Discarding event %s missing namespace/table", eventID)
		return nil
	}

	return &Event{
		EventID:   eventID,
		EventTime: eventTimeMicros,
		Namespace: namespace,
		Table:     table,
		Data:      dims,
	}
}

func extractEventTime(eventData map[string]interface{}) int64 {
	candidates := []interface{}{eventData["event_time"], eventData["eventTime"], eventData["time"]}
	for _, candidate := range candidates {
		switch v := candidate.(type) {
		case float64:
			if v == 0 {
				continue
			}
			ts := int64(v)
			return normalizeToMicros(ts)
		case int64:
			if v == 0 {
				continue
			}
			return normalizeToMicros(v)
		case json.Number:
			if val, err := v.Int64(); err == nil {
				return normalizeToMicros(val)
			}
		case string:
			if val, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return val.UnixMicro()
			}
		}
	}
	return time.Now().UnixMicro()
}

func normalizeToMicros(ts int64) int64 {
	switch {
	case ts <= 0:
		return time.Now().UnixMicro()
	case ts < 1_000_000_000_000: // likely seconds
		return ts * int64(time.Second/time.Microsecond)
	case ts < 1_000_000_000_000_000: // likely milliseconds
		return ts * int64(time.Millisecond/time.Microsecond)
	default:
		return ts
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
