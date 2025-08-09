package processor

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// EventProcessor handles event transformation and deduplication
type EventProcessor struct {
	batchSize    int
	dedupWindow  time.Duration
	dedupIndex   sync.Map // map[string]time.Time
	
	// Stats
	processed    atomic.Uint64
	deduplicated atomic.Uint64
	transformed  atomic.Uint64
}

// NewEventProcessor creates a new processor
func NewEventProcessor(batchSize int) *EventProcessor {
	ep := &EventProcessor{
		batchSize:   batchSize,
		dedupWindow: 24 * time.Hour,
	}
	
	// Start cleanup goroutine
	go ep.cleanupLoop()
	
	return ep
}

// ProcessBatch processes a batch of events
func (ep *EventProcessor) ProcessBatch(events []interface{}) []interface{} {
	processed := make([]interface{}, 0, len(events))
	
	for _, event := range events {
		// Generate dedup key
		dedupKey := ep.generateDedupKey(event)
		
		// Check for duplicate
		if ep.isDuplicate(dedupKey) {
			ep.deduplicated.Add(1)
			continue
		}
		
		// Transform event
		transformed := ep.transform(event)
		if transformed != nil {
			processed = append(processed, transformed)
			ep.transformed.Add(1)
		}
		
		ep.processed.Add(1)
	}
	
	return processed
}

// generateDedupKey creates a unique key for deduplication
func (ep *EventProcessor) generateDedupKey(event interface{}) string {
	// Use actual event data for dedup key
	if eventMap, ok := event.(map[string]interface{}); ok {
		if eventID, ok := eventMap["EventID"].(string); ok {
			// Use event ID as dedup key
			return eventID
		}
	}
	
	// Fallback to timestamp-based key
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), time.Now().Unix())
}

// isDuplicate checks if event is a duplicate
func (ep *EventProcessor) isDuplicate(key string) bool {
	now := time.Now()
	
	// Check if exists
	if val, exists := ep.dedupIndex.Load(key); exists {
		if timestamp, ok := val.(time.Time); ok {
			// Check if within dedup window
			if now.Sub(timestamp) < ep.dedupWindow {
				return true
			}
		}
	}
	
	// Store new entry
	ep.dedupIndex.Store(key, now)
	return false
}

// transform applies transformations to event
func (ep *EventProcessor) transform(event interface{}) interface{} {
	// Apply transformations
	// - Field mapping
	// - Type conversions
	// - Enrichment
	// - Filtering
	
	// For now, pass through
	return event
}

// cleanupLoop removes old dedup entries
func (ep *EventProcessor) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		cutoff := time.Now().Add(-ep.dedupWindow)
		removed := 0
		
		ep.dedupIndex.Range(func(key, value interface{}) bool {
			if timestamp, ok := value.(time.Time); ok {
				if timestamp.Before(cutoff) {
					ep.dedupIndex.Delete(key)
					removed++
				}
			}
			return true
		})
		
		if removed > 0 {
			log.Debugf("Cleaned up %d old dedup entries", removed)
		}
	}
}

// GetStats returns processor statistics
func (ep *EventProcessor) GetStats() map[string]uint64 {
	return map[string]uint64{
		"processed":    ep.processed.Load(),
		"deduplicated": ep.deduplicated.Load(),
		"transformed":  ep.transformed.Load(),
	}
}