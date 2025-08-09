package snapshot

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
)

type Config struct {
	Interval    time.Duration
	StoragePath string
	Compression bool
}

type Manager struct {
	config Config
}

type Snapshotter interface {
	GetSnapshot() (interface{}, error)
}

func NewManager(config Config) *Manager {
	// Create storage directory
	os.MkdirAll(config.StoragePath, 0755)
	
	return &Manager{
		config: config,
	}
}

func (m *Manager) Start(ctx context.Context, snapshotter Snapshotter) {
	ticker := time.NewTicker(m.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.TakeSnapshot(snapshotter); err != nil {
				log.Errorf("Failed to take snapshot: %v", err)
			}
		}
	}
}

func (m *Manager) TakeSnapshot(snapshotter Snapshotter) error {
	start := time.Now()
	
	// Get data from snapshotter
	data, err := snapshotter.GetSnapshot()
	if err != nil {
		return fmt.Errorf("failed to get snapshot data: %w", err)
	}

	// Create snapshot file
	filename := fmt.Sprintf("snapshot_%d.bin", time.Now().Unix())
	if m.config.Compression {
		filename += ".gz"
	}
	
	path := filepath.Join(m.config.StoragePath, filename)
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create snapshot file: %w", err)
	}
	defer file.Close()

	// Write data
	var encoder *gob.Encoder
	if m.config.Compression {
		gzWriter := gzip.NewWriter(file)
		defer gzWriter.Close()
		encoder = gob.NewEncoder(gzWriter)
	} else {
		encoder = gob.NewEncoder(file)
	}

	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}

	duration := time.Since(start)
	log.Infof("Snapshot saved to %s in %v", path, duration)
	
	// Clean old snapshots (keep last 10)
	m.cleanOldSnapshots()
	
	return nil
}

func (m *Manager) cleanOldSnapshots() {
	files, err := filepath.Glob(filepath.Join(m.config.StoragePath, "snapshot_*.bin*"))
	if err != nil {
		log.Errorf("Failed to list snapshots: %v", err)
		return
	}

	if len(files) > 10 {
		// Delete oldest snapshots
		for i := 0; i < len(files)-10; i++ {
			if err := os.Remove(files[i]); err != nil {
				log.Errorf("Failed to remove old snapshot %s: %v", files[i], err)
			}
		}
	}
}

func (m *Manager) LoadLatest() (interface{}, error) {
	files, err := filepath.Glob(filepath.Join(m.config.StoragePath, "snapshot_*.bin*"))
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no snapshots found")
	}

	// Get the latest file
	latestFile := files[len(files)-1]
	
	file, err := os.Open(latestFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot: %w", err)
	}
	defer file.Close()

	var decoder *gob.Decoder
	if filepath.Ext(latestFile) == ".gz" {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		decoder = gob.NewDecoder(gzReader)
	} else {
		decoder = gob.NewDecoder(file)
	}

	var data interface{}
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}

	log.Infof("Loaded snapshot from %s", latestFile)
	return data, nil
}