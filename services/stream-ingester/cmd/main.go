package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
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
	hotTierWriter := writer.NewHotTierWriter(config.HotTierEndpoint)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < config.NumWorkers; i++ {
		wg.Add(1)
		go worker(ctx, &wg, kafkaConsumer, eventProcessor, hotTierWriter, i)
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
	consumer *consumer.KafkaConsumer,
	processor *processor.EventProcessor,
	writer *writer.HotTierWriter,
	workerID int) {
	
	defer wg.Done()
	
	batch := make([]*Event, 0, 1000)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush remaining batch
			if len(batch) > 0 {
				processBatch(batch, processor, writer, workerID)
			}
			return

		case msg := <-consumer.Messages():
			// Parse event with headers
			event := parseKafkaMessage(msg)
			if event != nil {
				batch = append(batch, event)
			}

			// Process batch if full
			if len(batch) >= 1000 {
				processBatch(batch, processor, writer, workerID)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				processBatch(batch, processor, writer, workerID)
				batch = batch[:0]
			}
		}
	}
}

func processBatch(batch []*Event, processor *processor.EventProcessor, 
	writer *writer.HotTierWriter, workerID int) {
	
	start := time.Now()
	
	// Convert to interface slice as maps for the writer
	events := make([]interface{}, len(batch))
	for i, e := range batch {
		// Convert Event struct to map for writer
		events[i] = map[string]interface{}{
			"EventID":   e.EventID,
			"EventTime": e.EventTime,
			"Namespace": e.Namespace,
			"Table":     e.Table,
			"Data":      e.Data,
		}
	}
	
	// Process events (dedup, transform, etc)
	processed := processor.ProcessBatch(events)
	
	// Write to hot tier
	err := writer.WriteBatch(processed)
	if err != nil {
		log.Errorf("Worker %d: Failed to write batch: %v", workerID, err)
		return
	}
	
	duration := time.Since(start)
	log.Debugf("Worker %d: Processed %d events in %v", workerID, len(batch), duration)
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

	// Extract event fields
	eventID, _ := eventData["id"].(string)
	if eventID == "" {
		eventID = fmt.Sprintf("event-%d", time.Now().UnixNano())
	}

	eventTime, ok := eventData["time"].(float64)
	if !ok {
		// Try event_time field (expecting microseconds)
		eventTime, ok = eventData["event_time"].(float64)
		if !ok {
			eventTime = float64(time.Now().UnixMicro())
		}
	}

	// Extract dimensions
	dims, _ := eventData["dims"].(map[string]interface{})
	if dims == nil {
		dims = make(map[string]interface{})
	}

	return &Event{
		EventID:   eventID,
		EventTime: int64(eventTime), // In microseconds
		Namespace: namespace,
		Table:     table,
		Data:      dims,
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}