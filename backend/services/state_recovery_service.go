package services

import (
	"context"
	"content-moderation/config"
	"content-moderation/models"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type StateRecoveryService struct {
	db            *gorm.DB
	redis         *redis.Client
	config        *config.Config
	txManager     *models.StateTransactionManager
	recoveryTasks sync.Map
	isRunning     bool
}

type RecoveryTask struct {
	ID          uuid.UUID
	TaskID      uuid.UUID
	Action      string
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   time.Time
	LastAttempt *time.Time
	IsCompleted bool
	IsFailed    bool
}

type InconsistencyReport struct {
	TaskID          uuid.UUID
	CurrentStatus   string
	Inconsistencies map[string]interface{}
	DetectedAt      time.Time
}

func NewStateRecoveryService(db *gorm.DB, redis *redis.Client, cfg *config.Config, txManager *models.StateTransactionManager) *StateRecoveryService {
	return &StateRecoveryService{
		db:        db,
		redis:     redis,
		config:    cfg,
		txManager: txManager,
		isRunning: false,
	}
}

func (srs *StateRecoveryService) StartRecoveryScheduler(ctx context.Context) {
	if srs.isRunning {
		return
	}
	srs.isRunning = true

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				srs.isRunning = false
				return
			case <-ticker.C:
				srs.runRecoveryCheck(ctx)
			}
		}
	}()

	go func() {
		activeTxTicker := time.NewTicker(1 * time.Minute)
		defer activeTxTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-activeTxTicker.C:
				srs.checkStaleTransactions(ctx)
			}
		}
	}()
}

func (srs *StateRecoveryService) runRecoveryCheck(ctx context.Context) {
	var checkpoints []models.StateCheckpoint
	if err := srs.db.Where("status_consistent = ? OR last_checked_at < ?", 
		false, time.Now().Add(-30*time.Minute)).
		Limit(100).Find(&checkpoints).Error; err != nil {
		return
	}

	for _, checkpoint := range checkpoints {
		isConsistent, inconsistencies, err := srs.txManager.VerifyStateConsistency(ctx, checkpoint.TaskID)
		if err != nil {
			continue
		}

		if !isConsistent && inconsistencies != nil {
			srs.handleInconsistency(ctx, checkpoint.TaskID, inconsistencies)
		}
	}

	srs.processRecoveryTasks(ctx)
}

func (srs *StateRecoveryService) handleInconsistency(
	ctx context.Context,
	taskID uuid.UUID,
	inconsistencies map[string]interface{},
) {
	var task models.ModerationTask
	if err := srs.db.First(&task, taskID).Error; err != nil {
		return
	}

	report := &InconsistencyReport{
		TaskID:          taskID,
		CurrentStatus:   task.CurrentStatus,
		Inconsistencies: inconsistencies,
		DetectedAt:      time.Now(),
	}

	reportJSON, _ := json.Marshal(report)

	log := &models.ModerationLog{
		TaskID:    taskID,
		VideoID:   task.VideoID,
		ActorType: "system",
		Action:    "inconsistency_detected",
		Details: map[string]interface{}{
			"report": string(reportJSON),
		},
	}
	srs.db.Create(log)

	recoveryTask := &RecoveryTask{
		ID:          uuid.New(),
		TaskID:      taskID,
		Action:      "fix_inconsistency",
		Attempts:    0,
		MaxAttempts: 3,
		CreatedAt:   time.Now(),
		IsCompleted: false,
		IsFailed:    false,
	}

	srs.recoveryTasks.Store(recoveryTask.ID.String(), recoveryTask)

	if srs.redis != nil {
		recoveryKey := fmt.Sprintf("recovery:%s", recoveryTask.ID)
		recoveryData, _ := json.Marshal(recoveryTask)
		srs.redis.SetEX(ctx, recoveryKey, recoveryData, 24*time.Hour)
	}
}

func (srs *StateRecoveryService) processRecoveryTasks(ctx context.Context) {
	var tasksToProcess []*RecoveryTask

	srs.recoveryTasks.Range(func(key, value interface{}) bool {
		if task, ok := value.(*RecoveryTask); ok {
			if !task.IsCompleted && !task.IsFailed && task.Attempts < task.MaxAttempts {
				tasksToProcess = append(tasksToProcess, task)
			}
		}
		return true
	})

	for _, task := range tasksToProcess {
		success := srs.attemptRecovery(ctx, task)
		if success {
			task.IsCompleted = true
			srs.recoveryTasks.Store(task.ID.String(), task)
		} else {
			task.Attempts++
			now := time.Now()
			task.LastAttempt = &now
			
			if task.Attempts >= task.MaxAttempts {
				task.IsFailed = true
				srs.notifyFailure(ctx, task)
			}
			
			srs.recoveryTasks.Store(task.ID.String(), task)
		}
	}
}

func (srs *StateRecoveryService) attemptRecovery(
	ctx context.Context,
	recoveryTask *RecoveryTask,
) bool {
	var task models.ModerationTask
	if err := srs.db.First(&task, recoveryTask.TaskID).Error; err != nil {
		return false
	}

	switch recoveryTask.Action {
	case "fix_inconsistency":
		return srs.fixInconsistency(ctx, &task)
	
	case "rollback_stale_transaction":
		return srs.rollbackStaleTransaction(ctx, &task)
	
	case "fix_missing_human_result":
		return srs.fixMissingHumanResult(ctx, &task)
	
	case "fix_missing_auto_result":
		return srs.fixMissingAutoResult(ctx, &task)
	
	case "fix_status_mismatch":
		return srs.fixStatusMismatch(ctx, &task)
	
	default:
		return false
	}
}

func (srs *StateRecoveryService) fixInconsistency(
	ctx context.Context,
	task *models.ModerationTask,
) bool {
	_, inconsistencies, _ := srs.txManager.VerifyStateConsistency(ctx, task.ID)
	if inconsistencies == nil {
		return true
	}

	if _, ok := inconsistencies["missing_human_result"]; ok {
		return srs.fixMissingHumanResult(ctx, task)
	}

	if _, ok := inconsistencies["missing_auto_result"]; ok {
		return srs.fixMissingAutoResult(ctx, task)
	}

	if mismatch, ok := inconsistencies["status_mismatch"].(map[string]interface{}); ok {
		if checkpointStatus, ok := mismatch["checkpoint"].(string); ok {
			if checkpointStatus != "" {
				return srs.rollbackToConsistentState(ctx, task, checkpointStatus)
			}
		}
	}

	if _, ok := inconsistencies["task_video_status_mismatch"]; ok {
		return srs.syncTaskVideoStatus(ctx, task)
	}

	return srs.attemptRollbackToLatestSnapshot(ctx, task)
}

func (srs *StateRecoveryService) fixMissingHumanResult(
	ctx context.Context,
	task *models.ModerationTask,
) bool {
	var humanResult models.HumanModerationResult
	if err := srs.db.Where("task_id = ?", task.ID).First(&humanResult).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			if task.CurrentStatus == string(models.StatusHumanApproved) ||
				task.CurrentStatus == string(models.StatusHumanRejected) {
				
				var snapshot *models.StateSnapshot
				snapshots, err := srs.txManager.GetSnapshotHistory(ctx, task.ID, 10)
				if err == nil && len(snapshots) > 0 {
					for _, s := range snapshots {
						if s.Action == "human_approve" || s.Action == "human_reject" {
							snapshot = &s
							break
						}
					}
				}

				if snapshot != nil {
					decision := "approve"
					if snapshot.Action == "human_reject" {
						decision = "reject"
					}

					var violationTags []string
					if stateData, ok := snapshot.StateData.(map[string]interface{}); ok {
						if tags, ok := stateData["violation_tags"].([]string); ok {
							violationTags = tags
						}
					}

					newResult := &models.HumanModerationResult{
						TaskID:        task.ID,
						ReviewerID:    *snapshot.ActorID,
						Decision:      decision,
						ViolationTags: violationTags,
						IsAppeal:      false,
					}

					if err := srs.db.Create(newResult).Error; err == nil {
						log := &models.ModerationLog{
							TaskID:    task.ID,
							VideoID:   task.VideoID,
							ActorType: "system",
							Action:    "recovered_missing_human_result",
							Details: map[string]interface{}{
								"decision": decision,
								"source":   "snapshot",
							},
						}
						srs.db.Create(log)
						return true
					}
				}

				if task.CurrentStatus == string(models.StatusHumanApproved) {
					return srs.rollbackStatus(ctx, task, string(models.StatusInReview))
				} else {
					return srs.rollbackStatus(ctx, task, string(models.StatusInReview))
				}
			}
		}
	}
	return true
}

func (srs *StateRecoveryService) fixMissingAutoResult(
	ctx context.Context,
	task *models.ModerationTask,
) bool {
	var autoResult models.AutoModerationResult
	if err := srs.db.Where("task_id = ?", task.ID).First(&autoResult).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			if task.CurrentStatus == string(models.StatusAutoApproved) ||
				task.CurrentStatus == string(models.StatusAutoRejected) {
				
				if task.CurrentStatus == string(models.StatusAutoApproved) {
					return srs.rollbackStatus(ctx, task, string(models.StatusAutoModerating))
				} else {
					return srs.rollbackStatus(ctx, task, string(models.StatusAutoModerating))
				}
			}
		}
	}
	return true
}

func (srs *StateRecoveryService) rollbackStatus(
	ctx context.Context,
	task *models.ModerationTask,
	targetStatus string,
) bool {
	err := srs.db.Model(&models.ModerationTask{}).
		Where("id = ?", task.ID).
		Updates(map[string]interface{}{
			"current_status":  targetStatus,
			"previous_status": task.CurrentStatus,
		}).Error

	if err == nil {
		log := &models.ModerationLog{
			TaskID:     task.ID,
			VideoID:    task.VideoID,
			ActorType:  "system",
			Action:     "rollback_status",
			FromStatus: task.CurrentStatus,
			ToStatus:   targetStatus,
			Details: map[string]interface{}{
				"reason": "inconsistency_recovery",
			},
		}
		srs.db.Create(log)
	}

	return err == nil
}

func (srs *StateRecoveryService) rollbackToConsistentState(
	ctx context.Context,
	task *models.ModerationTask,
	consistentStatus string,
) bool {
	return srs.rollbackStatus(ctx, task, consistentStatus)
}

func (srs *StateRecoveryService) syncTaskVideoStatus(
	ctx context.Context,
	task *models.ModerationTask,
) bool {
	var video models.Video
	if err := srs.db.First(&video, task.VideoID).Error; err != nil {
		return false
	}

	if task.CurrentStatus == string(models.StatusPublished) && 
		video.Status != string(models.StatusPublished) {
		err := srs.db.Model(&models.Video{}).
			Where("id = ?", task.VideoID).
			Updates(map[string]interface{}{
				"status":       string(models.StatusPublished),
				"is_published": true,
			}).Error
		return err == nil
	}

	if task.CurrentStatus == string(models.StatusBanned) &&
		video.Status != string(models.StatusBanned) {
		err := srs.db.Model(&models.Video{}).
			Where("id = ?", task.VideoID).
			Updates(map[string]interface{}{
				"status":       string(models.StatusBanned),
				"is_published": false,
			}).Error
		return err == nil
	}

	return true
}

func (srs *StateRecoveryService) attemptRollbackToLatestSnapshot(
	ctx context.Context,
	task *models.ModerationTask,
) bool {
	snapshot, err := srs.txManager.GetLatestSnapshot(ctx, task.ID)
	if err != nil {
		return false
	}

	err = srs.txManager.RollbackSnapshot(ctx, snapshot)
	return err == nil
}

func (srs *StateRecoveryService) checkStaleTransactions(ctx context.Context) {
	if srs.redis == nil {
		return
	}

	keys, err := srs.redis.Keys(ctx, "state_transaction:*").Result()
	if err != nil {
		return
	}

	for _, key := range keys {
		var txCtx models.TransactionContext
		data, err := srs.redis.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		if err := json.Unmarshal([]byte(data), &txCtx); err != nil {
			continue
		}

		if time.Since(txCtx.StartTime) > 5*time.Minute && !txCtx.IsCompleted && !txCtx.IsRolledBack {
			srs.handleStaleTransaction(ctx, &txCtx)
		}
	}
}

func (srs *StateRecoveryService) handleStaleTransaction(
	ctx context.Context,
	txCtx *models.TransactionContext,
) {
	if len(txCtx.Snapshots) > 0 {
		err := srs.txManager.RollbackTransaction(ctx, txCtx)
		if err == nil {
			log := &models.ModerationLog{
				TaskID:    txCtx.TaskID,
				ActorType: "system",
				Action:    "stale_transaction_rolled_back",
				Details: map[string]interface{}{
					"transaction_id": txCtx.TransactionID,
					"action":         txCtx.Action,
					"stale_duration": time.Since(txCtx.StartTime).Seconds(),
				},
			}
			srs.db.Create(log)
		}
	}
}

func (srs *StateRecoveryService) notifyFailure(
	ctx context.Context,
	task *RecoveryTask,
) {
	notification := map[string]interface{}{
		"recovery_id":   task.ID,
		"task_id":       task.TaskID,
		"action":        task.Action,
		"attempts":      task.Attempts,
		"last_error":    task.LastError,
		"created_at":    task.CreatedAt,
		"failed_at":     time.Now(),
	}

	notifJSON, _ := json.Marshal(notification)

	log := &models.ModerationLog{
		TaskID:    task.TaskID,
		ActorType: "system",
		Action:    "recovery_failed",
		Details: map[string]interface{}{
			"notification": string(notifJSON),
		},
	}
	srs.db.Create(log)

	if srs.redis != nil {
		alertKey := fmt.Sprintf("alerts:recovery_failed:%s", task.ID)
		srs.redis.SetEX(ctx, alertKey, notifJSON, 7*24*time.Hour)
	}
}

func (srs *StateRecoveryService) ManualRollback(
	ctx context.Context,
	taskID uuid.UUID,
	actorID uuid.UUID,
	reason string,
) error {
	var task models.ModerationTask
	if err := srs.db.First(&task, taskID).Error; err != nil {
		return err
	}

	canRollback, reasonStr, err := srs.txManager.CanRollback(ctx, taskID)
	if err != nil {
		return err
	}
	if !canRollback {
		return fmt.Errorf("cannot rollback: %s", reasonStr)
	}

	snapshot, err := srs.txManager.GetLatestSnapshot(ctx, taskID)
	if err != nil {
		return err
	}

	err = srs.db.Transaction(func(tx *gorm.DB) error {
		if err := srs.txManager.RollbackSnapshot(ctx, snapshot); err != nil {
			return err
		}

		log := &models.ModerationLog{
			TaskID:     taskID,
			VideoID:    task.VideoID,
			ActorType:  "human",
			ActorID:    &actorID,
			Action:     "manual_rollback",
			FromStatus: snapshot.NewStatus,
			ToStatus:   snapshot.OldStatus,
			Details: map[string]interface{}{
				"reason":       reason,
				"snapshot_id":  snapshot.ID,
			},
		}
		return tx.Create(log).Error
	})

	return err
}

func (srs *StateRecoveryService) GetInconsistentTasks(
	ctx context.Context,
) ([]models.StateCheckpoint, error) {
	var checkpoints []models.StateCheckpoint
	if err := srs.db.Where("status_consistent = ?", false).
		Order("last_checked_at ASC").
		Find(&checkpoints).Error; err != nil {
		return nil, err
	}
	return checkpoints, nil
}

func (srs *StateRecoveryService) GetRecoveryTasks(
	ctx context.Context,
) ([]*RecoveryTask, error) {
	var tasks []*RecoveryTask

	srs.recoveryTasks.Range(func(key, value interface{}) bool {
		if task, ok := value.(*RecoveryTask); ok {
			tasks = append(tasks, task)
		}
		return true
	})

	return tasks, nil
}
