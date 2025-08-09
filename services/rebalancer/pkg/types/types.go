package types

import (
	"time"
)

// ShardMetrics represents real-time metrics for a shard
type ShardMetrics struct {
	ShardID           int32     `json:"shard_id"`
	RequestsPerSecond float64   `json:"requests_per_second"`
	BytesPerSecond    float64   `json:"bytes_per_second"`
	KeyCount          int64     `json:"key_count"`
	HotKeys           []HotKey  `json:"hot_keys,omitempty"`
	CPUUsage          float64   `json:"cpu_usage_percent"`
	MemoryUsage       float64   `json:"memory_usage_percent"`
	NetworkIn         float64   `json:"network_in_mbps"`
	NetworkOut        float64   `json:"network_out_mbps"`
	P99Latency        float64   `json:"p99_latency_ms"`
	ErrorRate         float64   `json:"error_rate"`
	LastUpdate        time.Time `json:"last_update"`
}

// HotKey represents a key that's receiving disproportionate traffic
type HotKey struct {
	Key               string  `json:"key"`
	RequestsPerSecond float64 `json:"requests_per_second"`
	BytesPerSecond    float64 `json:"bytes_per_second"`
	PercentOfShard    float64 `json:"percent_of_shard"` // What % of shard's load is this key
}

// ShardAssignment represents the assignment of keys to shards
type ShardAssignment struct {
	ShardID        int32             `json:"shard_id"`
	VirtualShards  []int32           `json:"virtual_shards"`  // Virtual shard IDs mapped to this physical shard
	KeyRangeStart  uint64            `json:"key_range_start"` // Hash range start
	KeyRangeEnd    uint64            `json:"key_range_end"`   // Hash range end
	SaltedKeys     map[string]string `json:"salted_keys"`     // Key -> Salt mapping for hot keys
	Replicas       []string          `json:"replicas"`        // Replica locations
	State          ShardState        `json:"state"`
	Owner          string            `json:"owner"`           // Service instance owning this shard
	Epoch          int64             `json:"epoch"`           // Configuration epoch
}

// ShardState represents the state of a shard
type ShardState int

const (
	ShardStateActive ShardState = iota
	ShardStateSplitting
	ShardStateMerging
	ShardStateMigrating
	ShardStateDraining
)

// RebalanceAction represents an action to rebalance load
type RebalanceAction struct {
	ID          string           `json:"id"`
	Type        ActionType       `json:"type"`
	SourceShard int32            `json:"source_shard"`
	TargetShard int32            `json:"target_shard,omitempty"`
	Keys        []string         `json:"keys,omitempty"`        // Specific keys to move
	SaltChanges map[string]string `json:"salt_changes,omitempty"` // Key -> NewSalt
	Reason      string           `json:"reason"`
	Priority    int              `json:"priority"` // Higher = more urgent
	CreatedAt   time.Time        `json:"created_at"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	Status      ActionStatus     `json:"status"`
	Error       string           `json:"error,omitempty"`
}

// ActionType defines the type of rebalance action
type ActionType int

const (
	ActionTypeSplitShard ActionType = iota
	ActionTypeMergeShard
	ActionTypeMigrateKeys
	ActionTypeAddSalt      // Add salt to hot keys
	ActionTypeRemoveSalt   // Remove salt from cooled keys
	ActionTypeRedistribute // Redistribute virtual shards
)

// ActionStatus represents the status of a rebalance action
type ActionStatus int

const (
	ActionStatusPending ActionStatus = iota
	ActionStatusInProgress
	ActionStatusCompleted
	ActionStatusFailed
	ActionStatusCancelled
)

// LoadDistribution represents the current load distribution across shards
type LoadDistribution struct {
	TotalRPS          float64                    `json:"total_rps"`
	TotalBPS          float64                    `json:"total_bps"` // Bytes per second
	ShardMetrics      map[int32]*ShardMetrics    `json:"shard_metrics"`
	ImbalanceRatio    float64                    `json:"imbalance_ratio"`    // Max/Avg load ratio
	GiniCoefficient   float64                    `json:"gini_coefficient"`   // 0=perfect equality, 1=perfect inequality
	HotShards         []int32                    `json:"hot_shards"`
	ColdShards        []int32                    `json:"cold_shards"`
	Recommendations   []RebalanceRecommendation  `json:"recommendations"`
	LastAnalysis      time.Time                  `json:"last_analysis"`
	AverageLoad       float64                    `json:"average_load"`       // Average RPS per shard
	StdDeviation      float64                    `json:"std_deviation"`      // Standard deviation of load
}

// RebalanceRecommendation suggests a rebalance action
type RebalanceRecommendation struct {
	Action          ActionType `json:"action"`
	ShardID         int32      `json:"shard_id"`
	TargetShardID   int32      `json:"target_shard_id,omitempty"`
	EstimatedImpact float64    `json:"estimated_impact"` // Expected improvement in balance
	Risk            RiskLevel  `json:"risk"`
	Reason          string     `json:"reason"`
}

// RiskLevel represents the risk of a rebalance action
type RiskLevel int

const (
	RiskLevelLow RiskLevel = iota
	RiskLevelMedium
	RiskLevelHigh
)

// EpochConfig represents a configuration epoch
type EpochConfig struct {
	Epoch           int64                       `json:"epoch"`
	Assignments     map[int32]*ShardAssignment  `json:"assignments"`
	VirtualToPhysical map[int32]int32           `json:"virtual_to_physical"` // Virtual shard -> Physical shard
	CreatedAt       time.Time                   `json:"created_at"`
	ActivatedAt     *time.Time                  `json:"activated_at,omitempty"`
	Reason          string                      `json:"reason"`
}

// HashingConfig defines how keys are hashed to shards
type HashingConfig struct {
	Algorithm         string            `json:"algorithm"`          // "xxhash", "murmur3", "fnv1a"
	VirtualShardCount int               `json:"virtual_shard_count"` // Number of virtual shards (should be >> physical shards)
	SaltPrefix        string            `json:"salt_prefix"`        // Prefix for salted keys
	ReplicationFactor int               `json:"replication_factor"`
	ConsistentHash    bool              `json:"consistent_hash"`    // Use consistent hashing
	PowerOfTwoChoices bool              `json:"power_of_two_choices"` // Use power of two choices for load balancing
}

// RebalancerConfig is the main configuration
type RebalancerConfig struct {
	// Detection thresholds
	HotShardThreshold      float64       `json:"hot_shard_threshold"`       // RPS ratio to avg (e.g., 1.5 = 50% above avg)
	ColdShardThreshold     float64       `json:"cold_shard_threshold"`      // RPS ratio to avg (e.g., 0.5 = 50% below avg)
	HotKeyThreshold        float64       `json:"hot_key_threshold"`         // % of shard traffic (e.g., 0.1 = 10%)
	ImbalanceThreshold     float64       `json:"imbalance_threshold"`       // Gini coefficient threshold
	
	// Action thresholds
	MinShardSize           int64         `json:"min_shard_size"`            // Minimum keys per shard before merge
	MaxShardSize           int64         `json:"max_shard_size"`            // Maximum keys per shard before split
	CooldownPeriod         time.Duration `json:"cooldown_period"`           // Wait between rebalance actions
	MaxConcurrentActions   int           `json:"max_concurrent_actions"`
	
	// Salting configuration
	AutoSaltHotKeys        bool          `json:"auto_salt_hot_keys"`
	SaltTTL                time.Duration `json:"salt_ttl"`                  // How long to keep salts
	MaxSaltedKeysPerShard  int           `json:"max_salted_keys_per_shard"`
	
	// Monitoring
	MetricsInterval        time.Duration `json:"metrics_interval"`
	AnalysisInterval       time.Duration `json:"analysis_interval"`
	
	// Safety
	MaxLoadShift           float64       `json:"max_load_shift"`            // Max % of load to move at once
	RequireApproval        bool          `json:"require_approval"`          // Require manual approval for actions
	DryRun                 bool          `json:"dry_run"`                   // Don't execute actions, just log
}

// ShardSplitPlan describes how to split a shard
type ShardSplitPlan struct {
	SourceShard    int32                     `json:"source_shard"`
	NewShards      []int32                   `json:"new_shards"`
	KeyDistribution map[int32][]string       `json:"key_distribution"` // Which keys go to which new shard
	VirtualShardSplit map[int32][]int32      `json:"virtual_shard_split"` // How to split virtual shards
	EstimatedBalance float64                 `json:"estimated_balance"` // Expected load balance after split
}

// ShardMergePlan describes how to merge shards
type ShardMergePlan struct {
	SourceShards   []int32  `json:"source_shards"`
	TargetShard    int32    `json:"target_shard"`
	TotalKeys      int64    `json:"total_keys"`
	EstimatedLoad  float64  `json:"estimated_load"` // Expected load after merge
}