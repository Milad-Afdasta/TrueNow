package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	log "github.com/sirupsen/logrus"
)

// Record represents a Kafka message alongside an acknowledgement function.
type Record struct {
	Message kafka.Message
	ack     func(context.Context) error
	once    sync.Once
}

// Ack commits the message offset exactly once. A nil context is treated as Background.
func (r *Record) Ack(ctx context.Context) error {
	if r == nil || r.ack == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	r.once.Do(func() {
		err = r.ack(ctx)
	})
	return err
}

// KafkaConsumer consumes events from Kafka with high throughput
type KafkaConsumer struct {
	readers  []*kafka.Reader
	messages chan *Record
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewKafkaConsumer creates a new Kafka consumer
func NewKafkaConsumer(brokers string, group string, topics []string) *KafkaConsumer {
	ctx, cancel := context.WithCancel(context.Background())

	kc := &KafkaConsumer{
		readers:  make([]*kafka.Reader, 0, len(topics)),
		messages: make(chan *Record, 10000), // Buffered for high throughput
		ctx:      ctx,
		cancel:   cancel,
	}

	// Create reader for each topic (for parallelism)
	for _, topic := range topics {
		reader := kafka.NewReader(kafka.ReaderConfig{
			Brokers:        []string{brokers},
			GroupID:        group,
			Topic:          topic,
			MinBytes:       1024,     // 1KB
			MaxBytes:       10485760, // 10MB
			CommitInterval: 1 * time.Second,
			StartOffset:    kafka.LastOffset,

			// Performance tuning
			QueueCapacity:    1000,
			MaxWait:          100 * time.Millisecond,
			ReadBatchTimeout: 10 * time.Millisecond,

			// Partition assignment handled by consumer group
			WatchPartitionChanges: true,
		})

		kc.readers = append(kc.readers, reader)

		// Start consumer goroutine
		kc.wg.Add(1)
		go kc.consume(reader)
	}

	log.Infof("Kafka consumer started for topics: %v", topics)

	return kc
}

// consume reads from Kafka and sends to channel
func (kc *KafkaConsumer) consume(reader *kafka.Reader) {
	defer kc.wg.Done()

	for {
		select {
		case <-kc.ctx.Done():
			return
		default:
			// Read message with timeout
			ctx, cancel := context.WithTimeout(kc.ctx, 5*time.Second)
			msg, err := reader.FetchMessage(ctx)
			cancel()

			if err != nil {
				if err == context.Canceled {
					return
				}
				// Don't log timeout errors when there are no messages - this is expected
				if err != context.DeadlineExceeded {
					log.Warnf("Failed to fetch message: %v", err)
				}
				continue
			}

			record := &Record{
				Message: msg,
				ack:     makeAckFunc(reader, msg),
			}

			// Send to processing channel
			select {
			case kc.messages <- record:
			case <-kc.ctx.Done():
				return
			}
		}
	}
}

// Messages returns the message channel
func (kc *KafkaConsumer) Messages() <-chan *Record {
	return kc.messages
}

// Close shuts down the consumer
func (kc *KafkaConsumer) Close() {
	kc.cancel()
	kc.wg.Wait()

	// Close all readers
	for _, reader := range kc.readers {
		if err := reader.Close(); err != nil {
			log.Errorf("Failed to close reader: %v", err)
		}
	}

	close(kc.messages)
	log.Info("Kafka consumer closed")
}

func makeAckFunc(reader *kafka.Reader, msg kafka.Message) func(context.Context) error {
	var once sync.Once
	var err error

	return func(ctx context.Context) error {
		once.Do(func() {
			if ctx == nil {
				ctx = context.Background()
			}
			err = reader.CommitMessages(ctx, msg)
			if err != nil {
				log.WithError(err).Warn("kafka consumer: commit failed")
			}
		})
		return err
	}
}

// GetStats returns consumer statistics
func (kc *KafkaConsumer) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	for i, reader := range kc.readers {
		readerStats := reader.Stats()
		stats[readerStats.Topic] = map[string]interface{}{
			"messages":  readerStats.Messages,
			"bytes":     readerStats.Bytes,
			"errors":    readerStats.Errors,
			"lag":       readerStats.Lag,
			"offset":    readerStats.Offset,
			"partition": readerStats.Partition,
		}

		log.Debugf("Reader %d stats: %+v", i, readerStats)
	}

	return stats
}
