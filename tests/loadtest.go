package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	EventID   string             `json:"event_id"`
	EventTime int64              `json:"event_time"`
	Revision  int                `json:"revision"`
	Dims      map[string]string  `json:"dims"`
	Metrics   map[string]float64 `json:"metrics"`
}

type Stats struct {
	sent      atomic.Uint64
	succeeded atomic.Uint64
	failed    atomic.Uint64
	latencyNs atomic.Uint64
	minLatency atomic.Uint64
	maxLatency atomic.Uint64
}

func main() {
	var (
		gateway     = flag.String("gateway", "http://localhost:8088", "Gateway URL")
		namespace   = flag.String("namespace", "analytics", "Namespace")
		table       = flag.String("table", "events", "Table name")
		rate        = flag.Int("rate", 100, "Events per second")
		duration    = flag.Duration("duration", 10*time.Second, "Test duration")
		concurrency = flag.Int("concurrency", 10, "Number of concurrent workers")
		batchSize   = flag.Int("batch", 1, "Events per request")
	)
	flag.Parse()

	fmt.Printf("Load Test Configuration:\n")
	fmt.Printf("  Gateway: %s\n", *gateway)
	fmt.Printf("  Target Rate: %d events/sec\n", *rate)
	fmt.Printf("  Duration: %v\n", *duration)
	fmt.Printf("  Concurrency: %d workers\n", *concurrency)
	fmt.Printf("  Batch Size: %d events/request\n", *batchSize)
	fmt.Println()

	stats := &Stats{}
	stats.minLatency.Store(^uint64(0))

	// Create HTTP client with connection pooling
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: *concurrency,
			MaxConnsPerHost:     *concurrency * 2,
		},
		Timeout: 5 * time.Second,
	}

	// Start workers
	var wg sync.WaitGroup
	ctx := make(chan bool)
	eventsPerWorker := *rate / *concurrency
	interval := time.Second / time.Duration(eventsPerWorker)

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go worker(i, client, *gateway, *namespace, *table, *batchSize, interval, ctx, stats, &wg)
	}

	// Start stats reporter
	go statsReporter(stats)

	// Run for duration
	time.Sleep(*duration)

	// Stop workers
	close(ctx)
	wg.Wait()

	// Final stats
	printFinalStats(stats, *duration)
}

func worker(id int, client *http.Client, gateway, namespace, table string, batchSize int, interval time.Duration, ctx chan bool, stats *Stats, wg *sync.WaitGroup) {
	defer wg.Done()
	
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	userID := fmt.Sprintf("user_%d", id)
	eventTypes := []string{"click", "view", "purchase", "share", "like"}

	for {
		select {
		case <-ctx:
			return
		case <-ticker.C:
			events := make([]Event, batchSize)
			for i := 0; i < batchSize; i++ {
				events[i] = Event{
					EventID:   fmt.Sprintf("evt_%d_%d_%d", id, time.Now().UnixNano(), i),
					EventTime: time.Now().UnixMilli(),
					Revision:  1,
					Dims: map[string]string{
						"user_id":    userID,
						"event_type": eventTypes[rand.Intn(len(eventTypes))],
						"page":       fmt.Sprintf("/page%d", rand.Intn(100)),
						"device":     "mobile",
					},
					Metrics: map[string]float64{
						"value":    float64(rand.Intn(1000)),
						"duration": rand.Float64() * 10,
						"score":    rand.Float64(),
					},
				}
			}

			sendEvents(client, gateway, namespace, table, events, stats)
		}
	}
}

func sendEvents(client *http.Client, gateway, namespace, table string, events []Event, stats *Stats) {
	stats.sent.Add(uint64(len(events)))
	start := time.Now()

	// Prepare request body
	var body []byte
	var err error
	if len(events) == 1 {
		body, err = json.Marshal(events[0])
	} else {
		body, err = json.Marshal(map[string][]Event{"events": events})
	}
	if err != nil {
		stats.failed.Add(uint64(len(events)))
		return
	}

	// Create request
	req, err := http.NewRequest("POST", gateway+"/v1/ingest", bytes.NewBuffer(body))
	if err != nil {
		stats.failed.Add(uint64(len(events)))
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Namespace", namespace)
	req.Header.Set("X-Table", table)

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		stats.failed.Add(uint64(len(events)))
		return
	}
	defer resp.Body.Close()

	// Update stats
	latency := uint64(time.Since(start).Nanoseconds())
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		stats.succeeded.Add(uint64(len(events)))
		stats.latencyNs.Add(latency)
		
		// Update min/max latency
		for {
			min := stats.minLatency.Load()
			if latency >= min || stats.minLatency.CompareAndSwap(min, latency) {
				break
			}
		}
		for {
			max := stats.maxLatency.Load()
			if latency <= max || stats.maxLatency.CompareAndSwap(max, latency) {
				break
			}
		}
	} else {
		stats.failed.Add(uint64(len(events)))
	}
}

func statsReporter(stats *Stats) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastSent, lastSucceeded, lastFailed uint64

	for range ticker.C {
		sent := stats.sent.Load()
		succeeded := stats.succeeded.Load()
		failed := stats.failed.Load()

		sentDelta := sent - lastSent
		succeededDelta := succeeded - lastSucceeded
		failedDelta := failed - lastFailed

		fmt.Printf("[%s] Sent: %d/s, Success: %d/s, Failed: %d/s\n",
			time.Now().Format("15:04:05"),
			sentDelta, succeededDelta, failedDelta)

		lastSent = sent
		lastSucceeded = succeeded
		lastFailed = failed
	}
}

func printFinalStats(stats *Stats, duration time.Duration) {
	sent := stats.sent.Load()
	succeeded := stats.succeeded.Load()
	failed := stats.failed.Load()
	totalLatency := stats.latencyNs.Load()
	minLatency := stats.minLatency.Load()
	maxLatency := stats.maxLatency.Load()

	fmt.Println("\n=== Final Statistics ===")
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("Total Events Sent: %d\n", sent)
	fmt.Printf("Total Succeeded: %d (%.2f%%)\n", succeeded, float64(succeeded)*100/float64(sent))
	fmt.Printf("Total Failed: %d (%.2f%%)\n", failed, float64(failed)*100/float64(sent))
	fmt.Printf("Average Rate: %.2f events/sec\n", float64(sent)/duration.Seconds())
	
	if succeeded > 0 {
		avgLatency := time.Duration(totalLatency / succeeded)
		fmt.Printf("Average Latency: %v\n", avgLatency)
		fmt.Printf("Min Latency: %v\n", time.Duration(minLatency))
		fmt.Printf("Max Latency: %v\n", time.Duration(maxLatency))
	}
}