package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrStateSnapshotNotFound = errors.New("state snapshot not found")
	ErrRollbackNotAllowed    = errors.New("rollback not allowed for current state")
	ErrCompensationFailed    = errors.New("compensation action failed")
	ErrStateInconsistent     = errors.New("state inconsistency detected")
)

type StateSnapshot struct {
	BaseModel
	TaskID         uuid.UUID `gorm:"not null;index" json:"task_id"`
	VideoID        uuid.UUID `gorm:"not null;index" json:"video_id"`
	OldStatus      string    `gorm:"not null;size:50" json:"old_status"`
	NewStatus      string    `gorm:"not null;size:50" json:"new_status"`
	OldVersion     int       `gorm:"not null" json:"old_version"`
	NewVersion     int       `gorm:"not null" json:"new_version"`
	Action         string    `gorm:"not null;size:100" json:"action"`
	ActorType      string    `gorm:"not null;size:20" json:"actor_type"`
	ActorID        *uuid.UUID `gorm:"index" json:"actor_id,omitempty"`
	StateData      JSON      `gorm:"type:jsonb" json:"state_data"`
	TransactionID  uuid.UUID `gorm:"not null;index" json:"transaction_id"`
	IsRolledBack   bool      `gorm:"default:false;index" json:"is_rolled_back"`
	RollbackAt     *time.Time `json:"rollback_at,omitempty"`
}

type CompensationAction struct {
	BaseModel
	SnapshotID    uuid.UUID `gorm:"not null;index" json:"snapshot_id"`
	ActionType    string    `gorm:"not null;size:50" json:"action_type"`
	ActionData    JSON      `gorm:"type:jsonb" json:"action_data"`
	IsExecuted    bool      `gorm:"default:false" json:"is_executed"`
	ExecutedAt    *time.Time `json:"executed_at,omitempty"`
	ExecutionResult string   `gorm:"type:text" json:"execution_result"`
}

type StateCheckpoint struct {
	BaseModel
	TaskID         uuid.UUID `gorm:"not null;uniqueIndex;index" json:"task_id"`
	CurrentStatus  string    `gorm:"not null;size:50" json:"current_status"`
	PreviousStatus string   `gorm:"size:50" json:"previous_status"`
	Version        int       `gorm:"not null" json:"version"`
	LastAction     string    `gorm:"size:100" json:"last_action"`
	LastActorType  string    `gorm:"size:20" json:"last_actor_type"`
	LastActorID    *uuid.UUID `json:"last_actor_id,omitempty"`
	StatusConsistent bool    `gorm:"default:true;index" json:"status_consistent"`
	LastCheckedAt  time.Time `gorm:"not null" json:"last_checked_at"`
	InconsistencyDetails JSON `gorm:"type:jsonb" json:"inconsistency_details,omitempty"`
}

type RollbackRule struct {
	FromStatus     ModerationStatus
	ToStatus       ModerationStatus
	AllowedActions []string
	Condition      func(task *ModerationTask, snapshot *StateSnapshot) bool
	Compensations  []string
}

type TransactionContext struct {
	TransactionID uuid.UUID
	StartTime     time.Time
	TaskID        uuid.UUID
	VideoID       uuid.UUID
	ActorID       *uuid.UUID
	ActorType     string
	Action        string
	Snapshots     []*StateSnapshot
	IsCompleted   bool
	IsRolledBack  bool
	Error         error
}

type StateTransactionManager struct {
	db           *gorm.DB
	redis        *redis.Client
	rollbackRules []RollbackRule
	txContexts    sync.Map
}

func NewStateTransactionManager(db *gorm.DB, redis *redis.Client) *StateTransactionManager {
	stm := &StateTransactionManager{
		db:        db,
		redis:     redis,
	}
	stm.initRollbackRules()
	return stm
}

func (stm *StateTransactionManager) initRollbackRules() {
	stm.rollbackRules = []RollbackRule{
		{
			FromStatus: StatusInReview,
			ToStatus:   StatusAssigned,
			AllowedActions: []string{"start_review"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "start_review"
			},
			Compensations: []string{"clear_assignee_lock", "restore_assigned_status"},
		},
		{
			FromStatus: StatusHumanApproved,
			ToStatus:   StatusInReview,
			AllowedActions: []string{"human_approve"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "human_approve"
			},
			Compensations: []string{"delete_human_result", "restore_in_review_status", "unpublish_video"},
		},
		{
			FromStatus: StatusHumanRejected,
			ToStatus:   StatusInReview,
			AllowedActions: []string{"human_reject"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "human_reject"
			},
			Compensations: []string{"delete_human_result", "restore_in_review_status", "unban_video"},
		},
		{
			FromStatus: StatusAutoApproved,
			ToStatus:   StatusAutoModerating,
			AllowedActions: []string{"auto_approve"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "auto_approve"
			},
			Compensations: []string{"delete_auto_result", "restore_auto_moderating_status"},
		},
		{
			FromStatus: StatusAutoRejected,
			ToStatus:   StatusAutoModerating,
			AllowedActions: []string{"auto_reject"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "auto_reject"
			},
			Compensations: []string{"delete_auto_result", "restore_auto_moderating_status"},
		},
		{
			FromStatus: StatusPublished,
			ToStatus:   StatusHumanApproved,
			AllowedActions: []string{"publish"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "publish"
			},
			Compensations: []string{"unpublish_video", "restore_human_approved_status"},
		},
		{
			FromStatus: StatusBanned,
			ToStatus:   StatusHumanRejected,
			AllowedActions: []string{"ban"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "ban"
			},
			Compensations: []string{"unban_video", "restore_human_rejected_status"},
		},
		{
			FromStatus: StatusAppealApproved,
			ToStatus:   StatusAppealed,
			AllowedActions: []string{"approve_appeal"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "approve_appeal"
			},
			Compensations: []string{"restore_appealed_status"},
		},
		{
			FromStatus: StatusAppealRejected,
			ToStatus:   StatusAppealed,
			AllowedActions: []string{"reject_appeal"},
			Condition: func(task *ModerationTask, snapshot *StateSnapshot) bool {
				if snapshot == nil {
					return true
				}
				return snapshot.Action == "reject_appeal"
			},
			Compensations: []string{"restore_appealed_status"},
		},
	}
}

func (stm *StateTransactionManager) BeginTransaction(
	ctx context.Context,
	taskID uuid.UUID,
	videoID uuid.UUID,
	actorID *uuid.UUID,
	actorType string,
	action string,
) (*TransactionContext, error) {
	txID := uuid.New()
	
	txCtx := &TransactionContext{
		TransactionID: txID,
		StartTime:     time.Now(),
		TaskID:        taskID,
		VideoID:       videoID,
		ActorID:       actorID,
		ActorType:     actorType,
		Action:        action,
		Snapshots:     make([]*StateSnapshot, 0),
		IsCompleted:   false,
		IsRolledBack:  false,
	}
	
	stm.txContexts.Store(txID.String(), txCtx)
	
	if stm.redis != nil {
		txKey := fmt.Sprintf("state_transaction:%s", txID)
		txData, _ := json.Marshal(txCtx)
		stm.redis.SetEX(ctx, txKey, txData, 30*time.Minute)
	}
	
	return txCtx, nil
}

func (stm *StateTransactionManager) CreateSnapshot(
	ctx context.Context,
	txCtx *TransactionContext,
	task *ModerationTask,
	oldStatus ModerationStatus,
	newStatus ModerationStatus,
	action string,
	stateData map[string]interface{},
) (*StateSnapshot, error) {
	snapshot := &StateSnapshot{
		TaskID:        task.ID,
		VideoID:       task.VideoID,
		OldStatus:     string(oldStatus),
		NewStatus:     string(newStatus),
		OldVersion:    task.Version,
		NewVersion:    task.Version + 1,
		Action:        action,
		ActorType:     txCtx.ActorType,
		ActorID:       txCtx.ActorID,
		StateData:     stateData,
		TransactionID: txCtx.TransactionID,
		IsRolledBack:  false,
	}
	
	if err := stm.db.Create(snapshot).Error; err != nil {
		return nil, err
	}
	
	txCtx.Snapshots = append(txCtx.Snapshots, snapshot)
	
	if stm.redis != nil {
		snapKey := fmt.Sprintf("snapshot:%s", snapshot.ID)
		snapData, _ := json.Marshal(snapshot)
		stm.redis.SetEX(ctx, snapKey, snapData, 24*time.Hour)
	}
	
	if err := stm.updateCheckpoint(ctx, task, action, txCtx.ActorType, txCtx.ActorID, true); err != nil {
		return snapshot, err
	}
	
	return snapshot, nil
}

func (stm *StateTransactionManager) CommitTransaction(
	ctx context.Context,
	txCtx *TransactionContext,
) error {
	txCtx.IsCompleted = true
	txCtx.Error = nil
	
	stm.txContexts.Delete(txCtx.TransactionID.String())
	
	if stm.redis != nil {
		txKey := fmt.Sprintf("state_transaction:%s", txCtx.TransactionID)
		stm.redis.Del(ctx, txKey)
	}
	
	return nil
}

func (stm *StateTransactionManager) RollbackTransaction(
	ctx context.Context,
	txCtx *TransactionContext,
) error {
	if txCtx.IsCompleted && !txCtx.IsRolledBack {
		return fmt.Errorf("cannot rollback committed transaction")
	}
	
	for i := len(txCtx.Snapshots) - 1; i >= 0; i-- {
		snapshot := txCtx.Snapshots[i]
		
		if err := stm.RollbackSnapshot(ctx, snapshot); err != nil {
			txCtx.Error = err
			return err
		}
	}
	
	txCtx.IsRolledBack = true
	stm.txContexts.Delete(txCtx.TransactionID.String())
	
	if stm.redis != nil {
		txKey := fmt.Sprintf("state_transaction:%s", txCtx.TransactionID)
		stm.redis.Del(ctx, txKey)
	}
	
	return nil
}

func (stm *StateTransactionManager) RollbackSnapshot(
	ctx context.Context,
	snapshot *StateSnapshot,
) error {
	var task ModerationTask
	if err := stm.db.First(&task, snapshot.TaskID).Error; err != nil {
		return err
	}
	
	if task.VideoID != snapshot.VideoID {
		return fmt.Errorf("task-video mismatch during rollback")
	}
	
	rule, err := stm.findRollbackRule(
		ModerationStatus(snapshot.NewStatus),
		ModerationStatus(snapshot.OldStatus),
	)
	if err != nil {
		return err
	}
	
	if !rule.Condition(&task, snapshot) {
		return ErrRollbackNotAllowed
	}
	
	err = stm.db.Transaction(func(tx *gorm.DB) error {
		for _, compAction := range rule.Compensations {
			if err := stm.executeCompensation(ctx, tx, compAction, &task, snapshot); err != nil {
				return err
			}
		}
		
		now := time.Now()
		snapshot.IsRolledBack = true
		snapshot.RollbackAt = &now
		if err := tx.Save(snapshot).Error; err != nil {
			return err
		}
		
		for _, compAction := range rule.Compensations {
			compensation := &CompensationAction{
				SnapshotID:    snapshot.ID,
				ActionType:    compAction,
				IsExecuted:    true,
				ExecutedAt:    &now,
				ExecutionResult: "success",
			}
			if err := tx.Create(compensation).Error; err != nil {
				return err
			}
		}
		
		if err := stm.updateCheckpoint(ctx, &task, "rollback", snapshot.ActorType, snapshot.ActorID, true); err != nil {
			return err
		}
		
		log := &ModerationLog{
			TaskID:     task.ID,
			VideoID:    task.VideoID,
			ActorType:  snapshot.ActorType,
			ActorID:    snapshot.ActorID,
			Action:     "rollback",
			FromStatus: snapshot.NewStatus,
			ToStatus:   snapshot.OldStatus,
			Details: map[string]interface{}{
				"snapshot_id":    snapshot.ID,
				"transaction_id": snapshot.TransactionID,
				"compensations":  rule.Compensations,
			},
		}
		return tx.Create(log).Error
	})
	
	if err != nil {
		return err
	}
	
	if stm.redis != nil {
		taskCacheKey := fmt.Sprintf("task:%s", task.ID)
		stm.redis.Del(ctx, taskCacheKey)
	}
	
	return nil
}

func (stm *StateTransactionManager) findRollbackRule(
	from ModerationStatus,
	to ModerationStatus,
) (*RollbackRule, error) {
	for _, rule := range stm.rollbackRules {
		if rule.FromStatus == from && rule.ToStatus == to {
			return &rule, nil
		}
	}
	return nil, fmt.Errorf("no rollback rule found for %s -> %s", from, to)
}

func (stm *StateTransactionManager) executeCompensation(
	ctx context.Context,
	tx *gorm.DB,
	action string,
	task *ModerationTask,
	snapshot *StateSnapshot,
) error {
	switch action {
	case "clear_assignee_lock":
		if stm.redis != nil {
			lockKey := fmt.Sprintf("task_review:%s", task.ID)
			stm.redis.Del(ctx, lockKey)
		}
		return nil
	
	case "restore_assigned_status":
		return tx.Model(&ModerationTask{}).
			Where("id = ? AND version = ?", task.ID, task.Version).
			Updates(map[string]interface{}{
				"current_status":  string(StatusAssigned),
				"previous_status": snapshot.OldStatus,
				"version":         snapshot.OldVersion,
			}).Error
	
	case "restore_in_review_status":
		return tx.Model(&ModerationTask{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"current_status":  string(StatusInReview),
				"previous_status": snapshot.OldStatus,
				"completed_at":    nil,
			}).Error
	
	case "delete_human_result":
		return tx.Where("task_id = ?", task.ID).
			Delete(&HumanModerationResult{}).Error
	
	case "delete_auto_result":
		return tx.Where("task_id = ?", task.ID).
			Delete(&AutoModerationResult{}).Error
	
	case "restore_auto_moderating_status":
		return tx.Model(&ModerationTask{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"current_status":       string(StatusAutoModerating),
				"previous_status":      snapshot.OldStatus,
				"auto_moderation_at":   nil,
			}).Error
	
	case "restore_human_approved_status":
		return tx.Model(&ModerationTask{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"current_status":  string(StatusHumanApproved),
				"previous_status": snapshot.OldStatus,
			}).Error
	
	case "restore_human_rejected_status":
		return tx.Model(&ModerationTask{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"current_status":  string(StatusHumanRejected),
				"previous_status": snapshot.OldStatus,
			}).Error
	
	case "restore_appealed_status":
		return tx.Model(&ModerationTask{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"current_status":  string(StatusAppealed),
				"previous_status": snapshot.OldStatus,
			}).Error
	
	case "unpublish_video":
		return tx.Model(&Video{}).
			Where("id = ?", task.VideoID).
			Updates(map[string]interface{}{
				"status":       string(StatusHumanApproved),
				"is_published": false,
			}).Error
	
	case "unban_video":
		return tx.Model(&Video{}).
			Where("id = ?", task.VideoID).
			Updates(map[string]interface{}{
				"status":       string(StatusHumanRejected),
				"is_published": false,
			}).Error
	
	default:
		return fmt.Errorf("unknown compensation action: %s", action)
	}
}

func (stm *StateTransactionManager) updateCheckpoint(
	ctx context.Context,
	task *ModerationTask,
	action string,
	actorType string,
	actorID *uuid.UUID,
	consistent bool,
) error {
	var checkpoint StateCheckpoint
	err := stm.db.Where("task_id = ?", task.ID).First(&checkpoint).Error
	
	if err == gorm.ErrRecordNotFound {
		checkpoint = StateCheckpoint{
			TaskID:         task.ID,
			CurrentStatus:  task.CurrentStatus,
			PreviousStatus: task.PreviousStatus,
			Version:        task.Version,
			LastAction:     action,
			LastActorType:  actorType,
			LastActorID:    actorID,
			StatusConsistent: consistent,
			LastCheckedAt:  time.Now(),
		}
		return stm.db.Create(&checkpoint).Error
	} else if err != nil {
		return err
	}
	
	checkpoint.CurrentStatus = task.CurrentStatus
	checkpoint.PreviousStatus = task.PreviousStatus
	checkpoint.Version = task.Version
	checkpoint.LastAction = action
	checkpoint.LastActorType = actorType
	checkpoint.LastActorID = actorID
	checkpoint.StatusConsistent = consistent
	checkpoint.LastCheckedAt = time.Now()
	
	return stm.db.Save(&checkpoint).Error
}

func (stm *StateTransactionManager) VerifyStateConsistency(
	ctx context.Context,
	taskID uuid.UUID,
) (bool, map[string]interface{}, error) {
	var task ModerationTask
	if err := stm.db.First(&task, taskID).Error; err != nil {
		return false, nil, err
	}
	
	var checkpoint StateCheckpoint
	if err := stm.db.Where("task_id = ?", taskID).First(&checkpoint).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return true, nil, nil
		}
		return false, nil, err
	}
	
	inconsistencies := make(map[string]interface{})
	
	if checkpoint.CurrentStatus != task.CurrentStatus {
		inconsistencies["status_mismatch"] = map[string]interface{}{
			"checkpoint": checkpoint.CurrentStatus,
			"actual":     task.CurrentStatus,
		}
	}
	
	if checkpoint.Version != task.Version {
		inconsistencies["version_mismatch"] = map[string]interface{}{
			"checkpoint": checkpoint.Version,
			"actual":     task.Version,
		}
	}
	
	var humanResultCount int64
	stm.db.Model(&HumanModerationResult{}).Where("task_id = ?", taskID).Count(&humanResultCount)
	
	if task.CurrentStatus == string(StatusHumanApproved) || task.CurrentStatus == string(StatusHumanRejected) {
		if humanResultCount == 0 {
			inconsistencies["missing_human_result"] = true
		}
	}
	
	var autoResultCount int64
	stm.db.Model(&AutoModerationResult{}).Where("task_id = ?", taskID).Count(&autoResultCount)
	
	if task.CurrentStatus == string(StatusAutoApproved) || task.CurrentStatus == string(StatusAutoRejected) {
		if autoResultCount == 0 {
			inconsistencies["missing_auto_result"] = true
		}
	}
	
	var video Video
	if err := stm.db.First(&video, task.VideoID).Error; err == nil {
		taskIsTerminal := task.CurrentStatus == string(StatusPublished) || 
			task.CurrentStatus == string(StatusBanned) ||
			task.CurrentStatus == string(StatusAppealRejected)
		
		videoIsTerminal := video.Status == string(StatusPublished) ||
			video.Status == string(StatusBanned)
		
		if taskIsTerminal != videoIsTerminal {
			inconsistencies["task_video_status_mismatch"] = map[string]interface{}{
				"task_status":  task.CurrentStatus,
				"video_status": video.Status,
			}
		}
	}
	
	if len(inconsistencies) > 0 {
		checkpoint.StatusConsistent = false
		checkpoint.InconsistencyDetails = inconsistencies
		checkpoint.LastCheckedAt = time.Now()
		stm.db.Save(&checkpoint)
		
		return false, inconsistencies, nil
	}
	
	checkpoint.StatusConsistent = true
	checkpoint.InconsistencyDetails = nil
	checkpoint.LastCheckedAt = time.Now()
	stm.db.Save(&checkpoint)
	
	return true, nil, nil
}

func (stm *StateTransactionManager) GetLatestSnapshot(
	ctx context.Context,
	taskID uuid.UUID,
) (*StateSnapshot, error) {
	var snapshot StateSnapshot
	if err := stm.db.Where("task_id = ? AND is_rolled_back = ?", taskID, false).
		Order("created_at DESC").
		First(&snapshot).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrStateSnapshotNotFound
		}
		return nil, err
	}
	return &snapshot, nil
}

func (stm *StateTransactionManager) GetSnapshotHistory(
	ctx context.Context,
	taskID uuid.UUID,
	limit int,
) ([]StateSnapshot, error) {
	var snapshots []StateSnapshot
	query := stm.db.Where("task_id = ?", taskID).
		Order("created_at DESC")
	
	if limit > 0 {
		query = query.Limit(limit)
	}
	
	if err := query.Find(&snapshots).Error; err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (stm *StateTransactionManager) CanRollback(
	ctx context.Context,
	taskID uuid.UUID,
) (bool, string, error) {
	var task ModerationTask
	if err := stm.db.First(&task, taskID).Error; err != nil {
		return false, "", err
	}
	
	snapshot, err := stm.GetLatestSnapshot(ctx, taskID)
	if err != nil {
		if err == ErrStateSnapshotNotFound {
			return false, "no snapshot available", nil
		}
		return false, "", err
	}
	
	_, err = stm.findRollbackRule(
		ModerationStatus(snapshot.NewStatus),
		ModerationStatus(snapshot.OldStatus),
	)
	if err != nil {
		return false, "no rollback rule for this transition", nil
	}
	
	return true, fmt.Sprintf("can rollback from %s to %s", 
		snapshot.NewStatus, snapshot.OldStatus), nil
}
