package models

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	LockPrefixTask         = "lock:task:"
	LockPrefixVideoFrame   = "lock:video_frame:"
	LockPrefixModeration   = "lock:moderation:"
	
	DefaultLockTTL         = 30 * time.Second
	DefaultLockWaitTimeout = 10 * time.Second
)

type TaskLock struct {
	TaskID     uuid.UUID `json:"task_id"`
	VideoID    uuid.UUID `json:"video_id"`
	LockHolder uuid.UUID `json:"lock_holder"`
	HolderType string    `json:"holder_type"`
	LockedAt   time.Time `json:"locked_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type DistributedLockService struct {
	redis *redis.Client
}

func NewDistributedLockService(redisClient *redis.Client) *DistributedLockService {
	return &DistributedLockService{
		redis: redisClient,
	}
}

func (dls *DistributedLockService) TryLockTask(ctx context.Context, taskID uuid.UUID, holderID uuid.UUID, holderType string) (*TaskLock, error) {
	lockKey := LockPrefixTask + taskID.String()
	
	lockData := TaskLock{
		TaskID:     taskID,
		LockHolder: holderID,
		HolderType: holderType,
		LockedAt:   time.Now(),
		ExpiresAt:  time.Now().Add(DefaultLockTTL),
	}
	
	success, err := dls.redis.SetNX(ctx, lockKey, lockData, DefaultLockTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}
	
	if !success {
		return nil, fmt.Errorf("task %s is already locked", taskID)
	}
	
	return &lockData, nil
}

func (dls *DistributedLockService) ReleaseTaskLock(ctx context.Context, taskID uuid.UUID) error {
	lockKey := LockPrefixTask + taskID.String()
	
	_, err := dls.redis.Del(ctx, lockKey).Result()
	return err
}

func (dls *DistributedLockService) ExtendTaskLock(ctx context.Context, taskID uuid.UUID, ttl time.Duration) error {
	lockKey := LockPrefixTask + taskID.String()
	
	return dls.redis.Expire(ctx, lockKey, ttl).Err()
}

func (dls *DistributedLockService) GetTaskLock(ctx context.Context, taskID uuid.UUID) (*TaskLock, error) {
	lockKey := LockPrefixTask + taskID.String()
	
	var lock TaskLock
	err := dls.redis.Get(ctx, lockKey).Scan(&lock)
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	
	return &lock, nil
}

func (dls *DistributedLockService) IsTaskLocked(ctx context.Context, taskID uuid.UUID) (bool, error) {
	lockKey := LockPrefixTask + taskID.String()
	
	exists, err := dls.redis.Exists(ctx, lockKey).Result()
	if err != nil {
		return false, err
	}
	
	return exists > 0, nil
}

func (dls *DistributedLockService) WaitForTaskLock(ctx context.Context, taskID uuid.UUID, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	
	for time.Now().Before(deadline) {
		locked, err := dls.IsTaskLocked(ctx, taskID)
		if err != nil {
			return err
		}
		
		if !locked {
			return nil
		}
		
		time.Sleep(100 * time.Millisecond)
	}
	
	return fmt.Errorf("timeout waiting for task lock: %s", taskID)
}

type FrameIndexMap struct {
	TaskID        uuid.UUID            `json:"task_id"`
	VideoID       uuid.UUID            `json:"video_id"`
	FrameCount    int                  `json:"frame_count"`
	FrameInterval float64              `json:"frame_interval"`
	Duration      float64              `json:"duration"`
	IndexToTime   map[int]float64     `json:"index_to_time"`
	TimeToIndex   map[float64]int     `json:"time_to_index"`
	CreatedAt     time.Time            `json:"created_at"`
}

type FrameMappingService struct {
	redis *redis.Client
}

func NewFrameMappingService(redisClient *redis.Client) *FrameMappingService {
	return &FrameMappingService{
		redis: redisClient,
	}
}

func (fms *FrameMappingService) getMappingKey(taskID uuid.UUID) string {
	return fmt.Sprintf("frame_mapping:task:%s", taskID)
}

func (fms *FrameMappingService) getFrameKey(taskID uuid.UUID, frameIndex int) string {
	return fmt.Sprintf("frame:task:%s:index:%d", taskID, frameIndex)
}

func (fms *FrameMappingService) getTimeIndexKey(taskID uuid.UUID) string {
	return fmt.Sprintf("frame_time_index:task:%s", taskID)
}

func (fms *FrameMappingService) CreateMapping(ctx context.Context, taskID uuid.UUID, videoID uuid.UUID, frames []VideoFrame) (*FrameIndexMap, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames provided")
	}
	
	indexToTime := make(map[int]float64)
	timeToIndex := make(map[float64]int)
	
	var minTime, maxTime float64
	for i, frame := range frames {
		indexToTime[frame.FrameIndex] = frame.Timestamp
		timeToIndex[frame.Timestamp] = frame.FrameIndex
		
		if i == 0 {
			minTime = frame.Timestamp
			maxTime = frame.Timestamp
		} else {
			if frame.Timestamp < minTime {
				minTime = frame.Timestamp
			}
			if frame.Timestamp > maxTime {
				maxTime = frame.Timestamp
			}
		}
	}
	
	var frameInterval float64
	if len(frames) > 1 {
		frameInterval = frames[1].Timestamp - frames[0].Timestamp
	}
	
	mapping := &FrameIndexMap{
		TaskID:        taskID,
		VideoID:       videoID,
		FrameCount:    len(frames),
		FrameInterval: frameInterval,
		Duration:      maxTime,
		IndexToTime:   indexToTime,
		TimeToIndex:   timeToIndex,
		CreatedAt:     time.Now(),
	}
	
	mappingKey := fms.getMappingKey(taskID)
	if err := fms.redis.Set(ctx, mappingKey, mapping, 24*time.Hour).Err(); err != nil {
		return nil, fmt.Errorf("failed to save frame mapping: %w", err)
	}
	
	timeIndexKey := fms.getTimeIndexKey(taskID)
	for timestamp, index := range timeToIndex {
		if err := fms.redis.ZAdd(ctx, timeIndexKey, &redis.Z{
			Score:  timestamp,
			Member: index,
		}).Err(); err != nil {
			return nil, fmt.Errorf("failed to save time index: %w", err)
		}
	}
	fms.redis.Expire(ctx, timeIndexKey, 24*time.Hour)
	
	return mapping, nil
}

func (fms *FrameMappingService) GetMapping(ctx context.Context, taskID uuid.UUID) (*FrameIndexMap, error) {
	mappingKey := fms.getMappingKey(taskID)
	
	var mapping FrameIndexMap
	err := fms.redis.Get(ctx, mappingKey).Scan(&mapping)
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	
	return &mapping, nil
}

func (fms *FrameMappingService) FindFrameByTime(ctx context.Context, taskID uuid.UUID, timestamp float64) (int, bool, error) {
	mapping, err := fms.GetMapping(ctx, taskID)
	if err != nil {
		return -1, false, err
	}
	
	if mapping == nil {
		return -1, false, nil
	}
	
	if timestamp <= 0 {
		if len(mapping.TimeToIndex) == 0 {
			return -1, false, nil
		}
		var minTime float64
		var minIndex int
		first := true
		for t, idx := range mapping.TimeToIndex {
			if first || t < minTime {
				minTime = t
				minIndex = idx
				first = false
			}
		}
		return minIndex, true, nil
	}
	
	if timestamp >= mapping.Duration {
		if len(mapping.TimeToIndex) == 0 {
			return -1, false, nil
		}
		var maxTime float64
		var maxIndex int
		first := true
		for t, idx := range mapping.TimeToIndex {
			if first || t > maxTime {
				maxTime = t
				maxIndex = idx
				first = false
			}
		}
		return maxIndex, true, nil
	}
	
	timeIndexKey := fms.getTimeIndexKey(taskID)
	
	results, err := fms.redis.ZRangeByScoreWithScores(ctx, timeIndexKey, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%f", timestamp),
		Offset: 0,
		Count:  1,
	}).Result()
	
	if err != nil {
		return -1, false, err
	}
	
	if len(results) == 0 {
		return -1, false, nil
	}
	
	var bestIndex int
	if index, ok := results[0].Member.(int); ok {
		bestIndex = index
	} else {
		return -1, false, fmt.Errorf("unexpected member type")
	}
	
	return bestIndex, true, nil
}

func (fms *FrameMappingService) GetTimeForFrame(ctx context.Context, taskID uuid.UUID, frameIndex int) (float64, bool, error) {
	mapping, err := fms.GetMapping(ctx, taskID)
	if err != nil {
		return 0, false, err
	}
	
	if mapping == nil {
		return 0, false, nil
	}
	
	timestamp, exists := mapping.IndexToTime[frameIndex]
	return timestamp, exists, nil
}

func (fms *FrameMappingService) DeleteMapping(ctx context.Context, taskID uuid.UUID) error {
	mappingKey := fms.getMappingKey(taskID)
	timeIndexKey := fms.getTimeIndexKey(taskID)
	
	return fms.redis.Del(ctx, mappingKey, timeIndexKey).Err()
}
