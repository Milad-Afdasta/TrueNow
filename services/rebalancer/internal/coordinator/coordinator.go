package coordinator

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/rebalancer/pkg/types"
)

// ShardCoordinator manages shard split/merge operations with epoch-based transitions
type ShardCoordinator struct {
	mu sync.RWMutex
	
	// Current epoch configuration
	currentEpoch    *types.EpochConfig
	nextEpoch       *types.EpochConfig
	epochCounter    atomic.Int64
	
	// Shard assignments
	assignments     map[int32]*types.ShardAssignment
	
	// Active rebalance actions
	activeActions   map[string]*types.RebalanceAction
	actionHistory   []*types.RebalanceAction
	
	// Configuration
	config          *types.RebalancerConfig
	
	// Callbacks for external systems
	onEpochChange   func(*types.EpochConfig) error
	onShardMigrate  func(from, to int32, keys []string) error
	
	// Statistics
	splitCount      atomic.Int64
	mergeCount      atomic.Int64
	migrationCount  atomic.Int64
	saltCount       atomic.Int64
}

// NewShardCoordinator creates a new shard coordinator
func NewShardCoordinator(config *types.RebalancerConfig) *ShardCoordinator {
	c := &ShardCoordinator{
		config:        config,
		assignments:   make(map[int32]*types.ShardAssignment),
		activeActions: make(map[string]*types.RebalanceAction),
		actionHistory: make([]*types.RebalanceAction, 0),
	}
	
	// Initialize with epoch 1
	c.currentEpoch = &types.EpochConfig{
		Epoch:             1,
		Assignments:       make(map[int32]*types.ShardAssignment),
		VirtualToPhysical: make(map[int32]int32),
		CreatedAt:         time.Now(),
		ActivatedAt:       &[]time.Time{time.Now()}[0],
		Reason:            "Initial configuration",
	}
	c.epochCounter.Store(1)
	
	return c
}

// PlanShardSplit plans a shard split operation
func (c *ShardCoordinator) PlanShardSplit(shardID int32, metrics *types.ShardMetrics) (*types.ShardSplitPlan, error) {
	c.mu.RLock()
	assignment, exists := c.assignments[shardID]
	c.mu.RUnlock()
	
	if !exists {
		return nil, fmt.Errorf("shard %d not found", shardID)
	}
	
	// Determine split count based on load
	splitCount := c.calculateOptimalSplitCount(metrics)
	
	plan := &types.ShardSplitPlan{
		SourceShard:       shardID,
		NewShards:         make([]int32, splitCount),
		KeyDistribution:   make(map[int32][]string),
		VirtualShardSplit: make(map[int32][]int32),
	}
	
	// Assign new shard IDs
	nextShardID := c.getNextShardID()
	for i := 0; i < splitCount; i++ {
		plan.NewShards[i] = nextShardID + int32(i)
		plan.KeyDistribution[plan.NewShards[i]] = []string{}
	}
	
	// Distribute virtual shards evenly
	virtualShards := assignment.VirtualShards
	shardsPerNew := len(virtualShards) / splitCount
	remainder := len(virtualShards) % splitCount
	
	vIndex := 0
	for i, newShard := range plan.NewShards {
		count := shardsPerNew
		if i < remainder {
			count++ // Distribute remainder
		}
		
		plan.VirtualShardSplit[newShard] = virtualShards[vIndex : vIndex+count]
		vIndex += count
	}
	
	// Estimate balance improvement
	plan.EstimatedBalance = 1.0 / float64(splitCount)
	
	return plan, nil
}

// ExecuteShardSplit executes a shard split with epoch transition
func (c *ShardCoordinator) ExecuteShardSplit(ctx context.Context, plan *types.ShardSplitPlan) error {
	// Create rebalance action
	action := &types.RebalanceAction{
		ID:          fmt.Sprintf("split-%d-%d", plan.SourceShard, time.Now().Unix()),
		Type:        types.ActionTypeSplitShard,
		SourceShard: plan.SourceShard,
		Reason:      "Shard overloaded",
		Priority:    10,
		CreatedAt:   time.Now(),
		Status:      types.ActionStatusPending,
	}
	
	c.mu.Lock()
	c.activeActions[action.ID] = action
	c.mu.Unlock()
	
	// Mark action as in progress
	now := time.Now()
	action.StartedAt = &now
	action.Status = types.ActionStatusInProgress
	
	// Prepare new epoch configuration
	newEpoch := c.prepareNewEpoch("Shard split")
	
	// Update assignments in new epoch
	for _, newShardID := range plan.NewShards {
		newAssignment := &types.ShardAssignment{
			ShardID:       newShardID,
			VirtualShards: plan.VirtualShardSplit[newShardID],
			State:         types.ShardStateActive,
			Epoch:         newEpoch.Epoch,
		}
		
		// Calculate key range based on virtual shards
		newAssignment.KeyRangeStart, newAssignment.KeyRangeEnd = c.calculateKeyRange(newAssignment.VirtualShards)
		
		newEpoch.Assignments[newShardID] = newAssignment
	}
	
	// Remove old shard from new epoch
	delete(newEpoch.Assignments, plan.SourceShard)
	
	// Update virtual to physical mapping
	for newShardID, virtualShards := range plan.VirtualShardSplit {
		for _, vShard := range virtualShards {
			newEpoch.VirtualToPhysical[vShard] = newShardID
		}
	}
	
	// Atomic epoch transition
	if err := c.transitionToEpoch(ctx, newEpoch); err != nil {
		action.Status = types.ActionStatusFailed
		action.Error = err.Error()
		return err
	}
	
	// Mark action as completed
	completedAt := time.Now()
	action.CompletedAt = &completedAt
	action.Status = types.ActionStatusCompleted
	
	c.splitCount.Add(1)
	
	// Add to history
	c.mu.Lock()
	delete(c.activeActions, action.ID)
	c.actionHistory = append(c.actionHistory, action)
	if len(c.actionHistory) > 100 {
		c.actionHistory = c.actionHistory[1:] // Keep last 100
	}
	c.mu.Unlock()
	
	return nil
}

// PlanShardMerge plans merging multiple shards
func (c *ShardCoordinator) PlanShardMerge(shardIDs []int32) (*types.ShardMergePlan, error) {
	if len(shardIDs) < 2 {
		return nil, fmt.Errorf("need at least 2 shards to merge")
	}
	
	c.mu.RLock()
	totalKeys := int64(0)
	totalLoad := 0.0
	
	for _, shardID := range shardIDs {
		assignment, exists := c.assignments[shardID]
		if !exists {
			c.mu.RUnlock()
			return nil, fmt.Errorf("shard %d not found", shardID)
		}
		
		// Estimate based on virtual shard count (simplified)
		totalKeys += int64(len(assignment.VirtualShards)) * 1000
		totalLoad += float64(len(assignment.VirtualShards))
	}
	c.mu.RUnlock()
	
	plan := &types.ShardMergePlan{
		SourceShards:  shardIDs,
		TargetShard:   shardIDs[0], // Use first shard as target
		TotalKeys:     totalKeys,
		EstimatedLoad: totalLoad,
	}
	
	return plan, nil
}

// ExecuteShardMerge executes a shard merge operation
func (c *ShardCoordinator) ExecuteShardMerge(ctx context.Context, plan *types.ShardMergePlan) error {
	// Create rebalance action
	action := &types.RebalanceAction{
		ID:          fmt.Sprintf("merge-%d-%d", plan.TargetShard, time.Now().Unix()),
		Type:        types.ActionTypeMergeShard,
		SourceShard: plan.SourceShards[0],
		TargetShard: plan.TargetShard,
		Reason:      "Shards underutilized",
		Priority:    5,
		CreatedAt:   time.Now(),
		Status:      types.ActionStatusPending,
	}
	
	c.mu.Lock()
	c.activeActions[action.ID] = action
	c.mu.Unlock()
	
	// Mark action as in progress
	now := time.Now()
	action.StartedAt = &now
	action.Status = types.ActionStatusInProgress
	
	// Prepare new epoch configuration
	newEpoch := c.prepareNewEpoch("Shard merge")
	
	// Collect all virtual shards from source shards
	allVirtualShards := []int32{}
	for _, shardID := range plan.SourceShards {
		if assignment, exists := c.currentEpoch.Assignments[shardID]; exists {
			allVirtualShards = append(allVirtualShards, assignment.VirtualShards...)
		}
	}
	
	// Create merged assignment
	mergedAssignment := &types.ShardAssignment{
		ShardID:       plan.TargetShard,
		VirtualShards: allVirtualShards,
		State:         types.ShardStateActive,
		Epoch:         newEpoch.Epoch,
	}
	mergedAssignment.KeyRangeStart, mergedAssignment.KeyRangeEnd = c.calculateKeyRange(allVirtualShards)
	
	// Update new epoch
	newEpoch.Assignments[plan.TargetShard] = mergedAssignment
	
	// Remove source shards (except target) from new epoch
	for _, shardID := range plan.SourceShards {
		if shardID != plan.TargetShard {
			delete(newEpoch.Assignments, shardID)
		}
	}
	
	// Update virtual to physical mapping
	for _, vShard := range allVirtualShards {
		newEpoch.VirtualToPhysical[vShard] = plan.TargetShard
	}
	
	// Atomic epoch transition
	if err := c.transitionToEpoch(ctx, newEpoch); err != nil {
		action.Status = types.ActionStatusFailed
		action.Error = err.Error()
		return err
	}
	
	// Mark action as completed
	completedAt := time.Now()
	action.CompletedAt = &completedAt
	action.Status = types.ActionStatusCompleted
	
	c.mergeCount.Add(1)
	
	// Add to history
	c.mu.Lock()
	delete(c.activeActions, action.ID)
	c.actionHistory = append(c.actionHistory, action)
	c.mu.Unlock()
	
	return nil
}

// AddSaltToHotKeys adds salts to hot keys for load distribution
func (c *ShardCoordinator) AddSaltToHotKeys(shardID int32, hotKeys []types.HotKey) error {
	if len(hotKeys) == 0 {
		return nil
	}
	
	// Check salt limit
	c.mu.RLock()
	assignment, exists := c.assignments[shardID]
	c.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("shard %d not found", shardID)
	}
	
	if len(assignment.SaltedKeys) >= c.config.MaxSaltedKeysPerShard {
		return fmt.Errorf("salt limit reached for shard %d", shardID)
	}
	
	// Create salt changes
	saltChanges := make(map[string]string)
	for i, hotKey := range hotKeys {
		if i >= c.config.MaxSaltedKeysPerShard-len(assignment.SaltedKeys) {
			break // Don't exceed limit
		}
		
		// Generate salt based on timestamp and index
		salt := fmt.Sprintf("%d-%d", time.Now().UnixNano(), i)
		saltChanges[hotKey.Key] = salt
	}
	
	// Create rebalance action
	action := &types.RebalanceAction{
		ID:          fmt.Sprintf("salt-%d-%d", shardID, time.Now().Unix()),
		Type:        types.ActionTypeAddSalt,
		SourceShard: shardID,
		SaltChanges: saltChanges,
		Reason:      "Hot keys detected",
		Priority:    8,
		CreatedAt:   time.Now(),
		Status:      types.ActionStatusInProgress,
	}
	
	now := time.Now()
	action.StartedAt = &now
	
	// Apply salts
	c.mu.Lock()
	if assignment.SaltedKeys == nil {
		assignment.SaltedKeys = make(map[string]string)
	}
	for key, salt := range saltChanges {
		assignment.SaltedKeys[key] = salt
	}
	c.mu.Unlock()
	
	// Mark as completed
	completedAt := time.Now()
	action.CompletedAt = &completedAt
	action.Status = types.ActionStatusCompleted
	
	c.saltCount.Add(int64(len(saltChanges)))
	
	// Add to history
	c.mu.Lock()
	c.actionHistory = append(c.actionHistory, action)
	c.mu.Unlock()
	
	return nil
}

// RemoveExpiredSalts removes salts that have expired
func (c *ShardCoordinator) RemoveExpiredSalts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	removedCount := 0
	
	for _, assignment := range c.assignments {
		if assignment.SaltedKeys == nil {
			continue
		}
		
		// Check each salt's age (simplified - in production would track creation time)
		for key := range assignment.SaltedKeys {
			// Remove salt after TTL
			delete(assignment.SaltedKeys, key)
			removedCount++
		}
	}
	
	return removedCount
}

// Helper methods

func (c *ShardCoordinator) prepareNewEpoch(reason string) *types.EpochConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	newEpoch := &types.EpochConfig{
		Epoch:             c.epochCounter.Load() + 1,
		Assignments:       make(map[int32]*types.ShardAssignment),
		VirtualToPhysical: make(map[int32]int32),
		CreatedAt:         time.Now(),
		Reason:            reason,
	}
	
	// Copy current assignments
	for shardID, assignment := range c.currentEpoch.Assignments {
		newAssignment := *assignment
		newAssignment.Epoch = newEpoch.Epoch
		newEpoch.Assignments[shardID] = &newAssignment
	}
	
	// Copy virtual mapping
	for vShard, pShard := range c.currentEpoch.VirtualToPhysical {
		newEpoch.VirtualToPhysical[vShard] = pShard
	}
	
	return newEpoch
}

func (c *ShardCoordinator) transitionToEpoch(ctx context.Context, newEpoch *types.EpochConfig) error {
	// Validate new epoch
	if err := c.validateEpoch(newEpoch); err != nil {
		return fmt.Errorf("epoch validation failed: %w", err)
	}
	
	// Call external callback if set
	if c.onEpochChange != nil {
		if err := c.onEpochChange(newEpoch); err != nil {
			return fmt.Errorf("epoch change callback failed: %w", err)
		}
	}
	
	// Atomic transition
	c.mu.Lock()
	c.nextEpoch = newEpoch
	
	// Wait for brief grace period
	time.Sleep(100 * time.Millisecond)
	
	// Activate new epoch
	now := time.Now()
	newEpoch.ActivatedAt = &now
	c.currentEpoch = newEpoch
	c.nextEpoch = nil
	c.epochCounter.Store(newEpoch.Epoch)
	
	// Update assignments
	c.assignments = make(map[int32]*types.ShardAssignment)
	for shardID, assignment := range newEpoch.Assignments {
		c.assignments[shardID] = assignment
	}
	
	c.mu.Unlock()
	
	return nil
}

func (c *ShardCoordinator) validateEpoch(epoch *types.EpochConfig) error {
	// Check all virtual shards are assigned
	virtualShards := make(map[int32]bool)
	for _, assignment := range epoch.Assignments {
		for _, vShard := range assignment.VirtualShards {
			if virtualShards[vShard] {
				return fmt.Errorf("virtual shard %d assigned multiple times", vShard)
			}
			virtualShards[vShard] = true
		}
	}
	
	// Check virtual to physical mapping consistency
	for _, pShard := range epoch.VirtualToPhysical {
		if _, exists := epoch.Assignments[pShard]; !exists {
			return fmt.Errorf("physical shard %d not found in assignments", pShard)
		}
	}
	
	return nil
}

func (c *ShardCoordinator) calculateOptimalSplitCount(metrics *types.ShardMetrics) int {
	// Base split count on load factor
	loadFactor := metrics.RequestsPerSecond / 10000.0 // Assuming 10K RPS is baseline per shard
	
	splitCount := int(math.Ceil(loadFactor))
	if splitCount < 2 {
		splitCount = 2
	}
	if splitCount > 4 {
		splitCount = 4 // Cap at 4-way split to avoid fragmentation
	}
	
	return splitCount
}

func (c *ShardCoordinator) calculateKeyRange(virtualShards []int32) (uint64, uint64) {
	if len(virtualShards) == 0 {
		return 0, 0
	}
	
	// Sort virtual shards
	sorted := make([]int32, len(virtualShards))
	copy(sorted, virtualShards)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	
	// Calculate range based on virtual shard IDs
	// In production, this would map to actual hash ranges
	rangeSize := uint64(math.MaxUint64) / uint64(1000) // Assuming 1000 max virtual shards
	start := uint64(sorted[0]) * rangeSize
	end := uint64(sorted[len(sorted)-1]+1) * rangeSize
	
	return start, end
}

func (c *ShardCoordinator) getNextShardID() int32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	maxID := int32(0)
	for shardID := range c.assignments {
		if shardID > maxID {
			maxID = shardID
		}
	}
	
	return maxID + 1
}

// GetCurrentEpoch returns the current epoch configuration
func (c *ShardCoordinator) GetCurrentEpoch() *types.EpochConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.currentEpoch
}

// GetStatistics returns coordinator statistics
func (c *ShardCoordinator) GetStatistics() map[string]interface{} {
	c.mu.RLock()
	activeCount := len(c.activeActions)
	historyCount := len(c.actionHistory)
	shardCount := len(c.assignments)
	c.mu.RUnlock()
	
	return map[string]interface{}{
		"current_epoch":    c.epochCounter.Load(),
		"shard_count":      shardCount,
		"split_count":      c.splitCount.Load(),
		"merge_count":      c.mergeCount.Load(),
		"migration_count":  c.migrationCount.Load(),
		"salt_count":       c.saltCount.Load(),
		"active_actions":   activeCount,
		"action_history":   historyCount,
	}
}