package models

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"content-moderation/config"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ModerationStatus string

const (
	StatusPending            ModerationStatus = "pending"
	StatusAutoModerating     ModerationStatus = "auto_moderating"
	StatusAutoApproved       ModerationStatus = "auto_approved"
	StatusAutoRejected       ModerationStatus = "auto_rejected"
	StatusNeedReview         ModerationStatus = "need_review"
	StatusAssigned           ModerationStatus = "assigned"
	StatusInReview           ModerationStatus = "in_review"
	StatusHumanApproved      ModerationStatus = "human_approved"
	StatusHumanRejected      ModerationStatus = "human_rejected"
	StatusAppealed           ModerationStatus = "appealed"
	StatusAppealApproved     ModerationStatus = "appeal_approved"
	StatusAppealRejected     ModerationStatus = "appeal_rejected"
	StatusPublished          ModerationStatus = "published"
	StatusBanned             ModerationStatus = "banned"
)

type StateTransition struct {
	From      ModerationStatus
	To        ModerationStatus
	Action    string
	Condition func(task *ModerationTask, data map[string]interface{}) bool
}

type TransitionResult struct {
	Success      bool
	OldStatus    string
	NewStatus    string
	OldVersion   int
	NewVersion   int
	TransitionID uuid.UUID
	Timestamp    time.Time
}

type StateMachine struct {
	db           *gorm.DB
	config       *config.Config
	redisClient  *redis.Client
	transitions  []StateTransition
	lock         sync.RWMutex
}

func NewStateMachine(db *gorm.DB, cfg *config.Config, redisClient *redis.Client) *StateMachine {
	sm := &StateMachine{
		db:          db,
		config:      cfg,
		redisClient: redisClient,
	}
	sm.initTransitions()
	return sm
}

func (sm *StateMachine) initTransitions() {
	sm.transitions = []StateTransition{
		{
			From:   StatusPending,
			To:     StatusAutoModerating,
			Action: "start_auto_moderation",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return task.AssignedTo == nil
			},
		},
		{
			From:   StatusAutoModerating,
			To:     StatusAutoApproved,
			Action: "auto_approve",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				score, ok := data["auto_score"].(float64)
				return ok && score <= sm.config.Moderation.AutoApproveThreshold
			},
		},
		{
			From:   StatusAutoModerating,
			To:     StatusAutoRejected,
			Action: "auto_reject",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				score, ok := data["auto_score"].(float64)
				return ok && score >= sm.config.Moderation.AutoRejectThreshold
			},
		},
		{
			From:   StatusAutoModerating,
			To:     StatusNeedReview,
			Action: "need_review",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				score, ok := data["auto_score"].(float64)
				return ok && score > sm.config.Moderation.AutoApproveThreshold && 
					   score < sm.config.Moderation.AutoRejectThreshold
			},
		},
		{
			From:   StatusNeedReview,
			To:     StatusAssigned,
			Action: "assign_reviewer",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				_, ok := data["reviewer_id"].(string)
				return ok
			},
		},
		{
			From:   StatusAssigned,
			To:     StatusInReview,
			Action: "start_review",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return task.AssignedTo != nil
			},
		},
		{
			From:   StatusInReview,
			To:     StatusHumanApproved,
			Action: "human_approve",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				decision, ok := data["decision"].(string)
				return ok && decision == "approve"
			},
		},
		{
			From:   StatusInReview,
			To:     StatusHumanRejected,
			Action: "human_reject",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				decision, ok := data["decision"].(string)
				return ok && decision == "reject"
			},
		},
		{
			From:   StatusAutoApproved,
			To:     StatusPublished,
			Action: "publish",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusHumanApproved,
			To:     StatusPublished,
			Action: "publish",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusAutoRejected,
			To:     StatusBanned,
			Action: "ban",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusHumanRejected,
			To:     StatusBanned,
			Action: "ban",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusAutoRejected,
			To:     StatusAppealed,
			Action: "submit_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				_, ok := data["appeal_reason"].(string)
				return ok
			},
		},
		{
			From:   StatusHumanRejected,
			To:     StatusAppealed,
			Action: "submit_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				_, ok := data["appeal_reason"].(string)
				return ok
			},
		},
		{
			From:   StatusAppealed,
			To:     StatusAppealApproved,
			Action: "approve_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				result, ok := data["appeal_result"].(string)
				return ok && result == "approve"
			},
		},
		{
			From:   StatusAppealed,
			To:     StatusAppealRejected,
			Action: "reject_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				result, ok := data["appeal_result"].(string)
				return ok && result == "reject"
			},
		},
		{
			From:   StatusAppealApproved,
			To:     StatusNeedReview,
			Action: "reassign_review",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
	}
}

func (sm *StateMachine) CanTransition(task *ModerationTask, to ModerationStatus, data map[string]interface{}) bool {
	for _, t := range sm.transitions {
		if ModerationStatus(task.CurrentStatus) == t.From && to == t.To {
			if t.Condition != nil {
				return t.Condition(task, data)
			}
			return true
		}
	}
	return false
}

func (sm *StateMachine) GetTaskWithLock(ctx context.Context, taskID uuid.UUID) (*ModerationTask, error) {
	var task ModerationTask
	
	if err := sm.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			First(&task, taskID).Error; err != nil {
			return err
		}
		
		if sm.redisClient != nil {
			lockKey := fmt.Sprintf("task_transition:%s", taskID)
			locked, err := sm.redisClient.SetNX(ctx, lockKey, "locked", 10*time.Second).Result()
			if err != nil {
				return err
			}
			if !locked {
				return ErrTaskAlreadyLocked
			}
		}
		
		return nil
	}); err != nil {
		return nil, err
	}
	
	return &task, nil
}

func (sm *StateMachine) Transition(
	ctx context.Context,
	task *ModerationTask,
	to ModerationStatus,
	data map[string]interface{},
	actorID *uuid.UUID,
	actorType string,
	videoID *uuid.UUID,
) (*TransitionResult, error) {
	
	if videoID != nil && task.VideoID != *videoID {
		return nil, ErrVideoTaskMismatch
	}
	
	if !sm.CanTransition(task, to, data) {
		return nil, fmt.Errorf("%w: cannot transition from %s to %s", 
			ErrStatusTransition, task.CurrentStatus, to)
	}
	
	oldStatus := task.CurrentStatus
	oldVersion := task.Version
	
	var result *TransitionResult
	
	if err := sm.db.Transaction(func(tx *gorm.DB) error {
		var currentTask ModerationTask
		if err := tx.First(&currentTask, task.ID).Error; err != nil {
			return err
		}
		
		if currentTask.Version != task.Version {
			return ErrOptimisticLockFailure
		}
		
		if currentTask.CurrentStatus != task.CurrentStatus {
			return fmt.Errorf("task status changed: expected %s but is %s", 
				task.CurrentStatus, currentTask.CurrentStatus)
		}
		
		now := time.Now()
		updates := map[string]interface{}{
			"previous_status": oldStatus,
			"current_status":  string(to),
			"version":         task.Version + 1,
		}
		
		switch to {
		case StatusAutoModerating:
			updates["auto_moderation_at"] = &now
		case StatusInReview:
			updates["human_review_at"] = &now
		case StatusPublished, StatusBanned, StatusAppealRejected:
			updates["completed_at"] = &now
		}
		
		if data != nil {
			if reviewerID, ok := data["reviewer_id"].(string); ok {
				rid, _ := uuid.Parse(reviewerID)
				updates["assigned_to"] = &rid
			}
		}
		
		updateResult := tx.Model(&ModerationTask{}).
			Where("id = ? AND version = ?", task.ID, oldVersion).
			Updates(updates)
		
		if updateResult.Error != nil {
			return updateResult.Error
		}
		
		if updateResult.RowsAffected == 0 {
			return ErrOptimisticLockFailure
		}
		
		task.CurrentStatus = string(to)
		task.PreviousStatus = oldStatus
		task.Version = oldVersion + 1
		
		var video Video
		if err := tx.First(&video, task.VideoID).Error; err != nil {
			return err
		}
		
		videoUpdates := map[string]interface{}{
			"status": string(to),
		}
		
		if to == StatusPublished {
			videoUpdates["is_published"] = true
		} else if to == StatusBanned {
			videoUpdates["is_published"] = false
		}
		
		if err := tx.Model(&video).Updates(videoUpdates).Error; err != nil {
			return err
		}
		
		var action string
		for _, t := range sm.transitions {
			if ModerationStatus(oldStatus) == t.From && to == t.To {
				action = t.Action
				break
			}
		}
		
		log := &ModerationLog{
			TaskID:     task.ID,
			VideoID:    task.VideoID,
			ActorType:  actorType,
			ActorID:    actorID,
			Action:     action,
			FromStatus: oldStatus,
			ToStatus:   string(to),
			Details:    data,
		}
		
		if err := tx.Create(log).Error; err != nil {
			return err
		}
		
		result = &TransitionResult{
			Success:      true,
			OldStatus:    oldStatus,
			NewStatus:    string(to),
			OldVersion:   oldVersion,
			NewVersion:   task.Version,
			TransitionID: uuid.New(),
			Timestamp:    now,
		}
		
		return nil
	}); err != nil {
		return nil, err
	}
	
	if sm.redisClient != nil {
		lockKey := fmt.Sprintf("task_transition:%s", task.ID)
		sm.redisClient.Del(ctx, lockKey)
		
		taskCacheKey := fmt.Sprintf("task:%s", task.ID)
		taskCacheData := map[string]interface{}{
			"id":              task.ID,
			"video_id":        task.VideoID,
			"current_status":  task.CurrentStatus,
			"previous_status": task.PreviousStatus,
			"version":         task.Version,
		}
		sm.redisClient.HSet(ctx, taskCacheKey, taskCacheData)
		sm.redisClient.Expire(ctx, taskCacheKey, 5*time.Minute)
	}
	
	return result, nil
}

func (sm *StateMachine) TryTransitionWithLock(
	ctx context.Context,
	taskID uuid.UUID,
	to ModerationStatus,
	data map[string]interface{},
	actorID *uuid.UUID,
	actorType string,
) (*TransitionResult, error) {
	
	if sm.redisClient != nil {
		lockKey := fmt.Sprintf("task_lock:%s", taskID)
		lockValue := uuid.New().String()
		lockTTL := 30 * time.Second
		
		locked, err := sm.redisClient.SetNX(ctx, lockKey, lockValue, lockTTL).Result()
		if err != nil {
			return nil, err
		}
		if !locked {
			return nil, ErrTaskAlreadyLocked
		}
		
		defer func() {
			val, _ := sm.redisClient.Get(ctx, lockKey).Result()
			if val == lockValue {
				sm.redisClient.Del(ctx, lockKey)
			}
		}()
	}
	
	var task ModerationTask
	if err := sm.db.First(&task, taskID).Error; err != nil {
		return nil, err
	}
	
	return sm.Transition(ctx, &task, to, data, actorID, actorType, nil)
}

func (sm *StateMachine) GetAvailableTransitions(task *ModerationTask, data map[string]interface{}) []ModerationStatus {
	var available []ModerationStatus
	for _, t := range sm.transitions {
		if ModerationStatus(task.CurrentStatus) == t.From {
			if t.Condition == nil || t.Condition(task, data) {
				available = append(available, t.To)
			}
		}
	}
	return available
}

func (sm *StateMachine) IsTerminal(status ModerationStatus) bool {
	terminalStatuses := []ModerationStatus{
		StatusPublished,
		StatusBanned,
		StatusAppealRejected,
	}
	for _, ts := range terminalStatuses {
		if status == ts {
			return true
		}
	}
	return false
}

func (sm *StateMachine) ValidateTaskVideoBinding(taskID uuid.UUID, videoID uuid.UUID) error {
	var task ModerationTask
	if err := sm.db.First(&task, taskID).Error; err != nil {
		return err
	}
	
	return task.ValidateVideoID(videoID)
}

func (sm *StateMachine) CheckConcurrentAccess(ctx context.Context, taskID uuid.UUID, actorID uuid.UUID) error {
	if sm.redisClient == nil {
		return nil
	}
	
	lockKey := fmt.Sprintf("task_review:%s", taskID)
	
	existingHolder, err := sm.redisClient.Get(ctx, lockKey).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	
	if existingHolder != actorID.String() {
		return fmt.Errorf("task %s is being reviewed by another user: %s", taskID, existingHolder)
	}
	
	return nil
}

func (sm *StateMachine) AcquireReviewLock(ctx context.Context, taskID uuid.UUID, actorID uuid.UUID, ttl time.Duration) error {
	if sm.redisClient == nil {
		return nil
	}
	
	lockKey := fmt.Sprintf("task_review:%s", taskID)
	
	existingHolder, err := sm.redisClient.Get(ctx, lockKey).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	
	if existingHolder != "" && existingHolder != actorID.String() {
		return ErrTaskAlreadyLocked
	}
	
	return sm.redisClient.Set(ctx, lockKey, actorID.String(), ttl).Err()
}

func (sm *StateMachine) ReleaseReviewLock(ctx context.Context, taskID uuid.UUID, actorID uuid.UUID) error {
	if sm.redisClient == nil {
		return nil
	}
	
	lockKey := fmt.Sprintf("task_review:%s", taskID)
	
	existingHolder, err := sm.redisClient.Get(ctx, lockKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return err
	}
	
	if existingHolder == actorID.String() {
		return sm.redisClient.Del(ctx, lockKey).Err()
	}
	
	return errors.New("not the lock holder")
}

func CalculateSLADeadline(cfg *config.Config) time.Time {
	return time.Now().Add(time.Duration(cfg.Moderation.SLAHours) * time.Hour)
}

func IsValidModerationStatus(status string) bool {
	validStatuses := []ModerationStatus{
		StatusPending,
		StatusAutoModerating,
		StatusAutoApproved,
		StatusAutoRejected,
		StatusNeedReview,
		StatusAssigned,
		StatusInReview,
		StatusHumanApproved,
		StatusHumanRejected,
		StatusAppealed,
		StatusAppealApproved,
		StatusAppealRejected,
		StatusPublished,
		StatusBanned,
	}
	
	for _, s := range validStatuses {
		if string(s) == status {
			return true
		}
	}
	return false
}
