package services

import (
	"context"
	"content-moderation/config"
	"content-moderation/models"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type HumanModerationServiceV2 struct {
	db            *gorm.DB
	redis         *redis.Client
	config        *config.Config
	stateMachine  *models.StateMachine
	txManager     *models.StateTransactionManager
}

type ReviewContext struct {
	TaskID         uuid.UUID
	ReviewerID     uuid.UUID
	Reviewer       *models.User
	Task           *models.ModerationTask
	Video          *models.Video
	AutoResult     *models.AutoModerationResult
	TxCtx          *models.TransactionContext
	StartSnapshot  *models.StateSnapshot
}

func NewHumanModerationServiceV2(
	db *gorm.DB, 
	redis *redis.Client, 
	cfg *config.Config, 
	sm *models.StateMachine,
	txManager *models.StateTransactionManager,
) *HumanModerationServiceV2 {
	return &HumanModerationServiceV2{
		db:           db,
		redis:        redis,
		config:       cfg,
		stateMachine: sm,
		txManager:    txManager,
	}
}

func (hms *HumanModerationServiceV2) StartReview(
	ctx context.Context,
	taskID uuid.UUID,
	reviewerID uuid.UUID,
) error {
	var task models.ModerationTask
	if err := hms.db.Preload("Assignee").Preload("Video").First(&task, taskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("task not found: %v", taskID)
		}
		return err
	}

	if err := hms.validateStartReview(&task, reviewerID); err != nil {
		return err
	}

	txCtx, err := hms.txManager.BeginTransaction(
		ctx,
		task.ID,
		task.VideoID,
		&reviewerID,
		"human",
		"start_review",
	)
	if err != nil {
		return err
	}

	err = hms.db.Transaction(func(tx *gorm.DB) error {
		if hms.redis != nil {
			lockKey := fmt.Sprintf("task_review:%s", task.ID)
			locked, err := hms.redis.SetNX(ctx, lockKey, reviewerID.String(), 30*time.Minute).Result()
			if err != nil {
				return err
			}
			if !locked {
				return fmt.Errorf("task is already being reviewed by another user")
			}
		}

		oldStatus := models.ModerationStatus(task.CurrentStatus)
		newStatus := models.StatusInReview

		data := map[string]interface{}{
			"reviewer_id":  reviewerID,
			"action":       "start_review",
		}

		_, err = hms.txManager.CreateSnapshot(
			ctx,
			txCtx,
			&task,
			oldStatus,
			newStatus,
			"start_review",
			data,
		)
		if err != nil {
			return err
		}

		now := time.Now()
		if err := tx.Model(&models.ModerationTask{}).
			Where("id = ? AND version = ?", task.ID, task.Version).
			Updates(map[string]interface{}{
				"current_status":   string(models.StatusInReview),
				"previous_status":  task.CurrentStatus,
				"human_review_at":  &now,
				"version":          task.Version + 1,
			}).Error; err != nil {
			return err
		}

		if err := hms.createModerationLog(
			tx,
			&task,
			"start_review",
			string(oldStatus),
			string(newStatus),
			"human",
			&reviewerID,
			map[string]interface{}{
				"transaction_id": txCtx.TransactionID,
			},
		); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		hms.txManager.RollbackTransaction(ctx, txCtx)
		if hms.redis != nil {
			lockKey := fmt.Sprintf("task_review:%s", task.ID)
			hms.redis.Del(ctx, lockKey)
		}
		return err
	}

	if err := hms.txManager.CommitTransaction(ctx, txCtx); err != nil {
		return err
	}

	_, _, err = hms.txManager.VerifyStateConsistency(ctx, taskID)
	return err
}

func (hms *HumanModerationServiceV2) validateStartReview(
	task *models.ModerationTask,
	reviewerID uuid.UUID,
) error {
	if task.AssignedTo == nil {
		return fmt.Errorf("task is not assigned to any reviewer")
	}
	
	if *task.AssignedTo != reviewerID {
		return fmt.Errorf("task is not assigned to this reviewer")
	}

	if task.CurrentStatus != string(models.StatusAssigned) {
		return fmt.Errorf("cannot start review: task is in status %s", task.CurrentStatus)
	}

	return nil
}

func (hms *HumanModerationServiceV2) SubmitReview(
	ctx context.Context,
	taskID uuid.UUID,
	reviewerID uuid.UUID,
	decision string,
	violationTags []string,
	comment string,
	reviewDuration float64,
) error {
	if err := hms.validateDecision(decision); err != nil {
		return err
	}

	reviewCtx, err := hms.prepareReviewContext(ctx, taskID, reviewerID)
	if err != nil {
		return err
	}

	txCtx, err := hms.txManager.BeginTransaction(
		ctx,
		taskID,
		reviewCtx.Video.ID,
		&reviewerID,
		"human",
		fmt.Sprintf("human_%s", decision),
	)
	if err != nil {
		return err
	}

	var newStatus models.ModerationStatus
	if decision == "approve" {
		newStatus = models.StatusHumanApproved
	} else {
		newStatus = models.StatusHumanRejected
	}

	err = hms.db.Transaction(func(tx *gorm.DB) error {
		oldStatus := models.ModerationStatus(reviewCtx.Task.CurrentStatus)

		stateData := map[string]interface{}{
			"decision":       decision,
			"violation_tags": violationTags,
			"comment":        comment,
			"reviewer_id":    reviewerID,
		}

		_, err := hms.txManager.CreateSnapshot(
			ctx,
			txCtx,
			reviewCtx.Task,
			oldStatus,
			newStatus,
			fmt.Sprintf("human_%s", decision),
			stateData,
		)
		if err != nil {
			return err
		}

		now := time.Now()
		humanResult := &models.HumanModerationResult{
			TaskID:         taskID,
			ReviewerID:     reviewerID,
			Decision:       decision,
			ViolationTags:  violationTags,
			Comment:        comment,
			ReviewDuration: reviewDuration,
			IsAppeal:       false,
			CreatedAt:      now,
		}

		if err := tx.Create(humanResult).Error; err != nil {
			return err
		}

		if err := tx.Model(&models.ModerationTask{}).
			Where("id = ? AND version = ?", taskID, reviewCtx.Task.Version).
			Updates(map[string]interface{}{
				"current_status":   string(newStatus),
				"previous_status":  reviewCtx.Task.CurrentStatus,
				"human_review_at":  &now,
				"completed_at":     &now,
				"version":          reviewCtx.Task.Version + 1,
			}).Error; err != nil {
			return err
		}

		var videoStatus string
		var isPublished bool
		if decision == "approve" {
			videoStatus = string(models.StatusHumanApproved)
			isPublished = true
		} else {
			videoStatus = string(models.StatusHumanRejected)
			isPublished = false
		}

		if err := tx.Model(&models.Video{}).
			Where("id = ?", reviewCtx.Video.ID).
			Updates(map[string]interface{}{
				"status":       videoStatus,
				"moderated_at": &now,
				"is_published": isPublished,
			}).Error; err != nil {
			return err
		}

		if err := hms.updateReviewerPerformanceTx(tx, reviewerID, decision); err != nil {
			return err
		}

		if err := hms.updateDailyStatisticsTx(tx, decision, reviewDuration); err != nil {
			return err
		}

		if err := hms.createModerationLog(
			tx,
			reviewCtx.Task,
			fmt.Sprintf("human_%s", decision),
			string(oldStatus),
			string(newStatus),
			"human",
			&reviewerID,
			map[string]interface{}{
				"transaction_id":    txCtx.TransactionID,
				"decision":          decision,
				"violation_tags":    violationTags,
				"comment":           comment,
				"human_result_id":   humanResult.ID,
				"review_duration":   reviewDuration,
			},
		); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		rollbackErr := hms.txManager.RollbackTransaction(ctx, txCtx)
		if rollbackErr != nil {
			return fmt.Errorf("submit failed: %v, rollback also failed: %v", err, rollbackErr)
		}
		return err
	}

	if err := hms.txManager.CommitTransaction(ctx, txCtx); err != nil {
		return err
	}

	if hms.redis != nil {
		lockKey := fmt.Sprintf("task_review:%s", taskID)
		hms.redis.Del(ctx, lockKey)
	}

	isConsistent, inconsistencies, err := hms.txManager.VerifyStateConsistency(ctx, taskID)
	if err != nil {
		return err
	}
	if !isConsistent {
		return fmt.Errorf("state inconsistency detected after submit: %v", inconsistencies)
	}

	return nil
}

func (hms *HumanModerationServiceV2) prepareReviewContext(
	ctx context.Context,
	taskID uuid.UUID,
	reviewerID uuid.UUID,
) (*ReviewContext, error) {
	var task models.ModerationTask
	if err := hms.db.Preload("Video").Preload("Assignee").First(&task, taskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("task not found: %v", taskID)
		}
		return nil, err
	}

	if task.AssignedTo == nil || *task.AssignedTo != reviewerID {
		return nil, fmt.Errorf("task not assigned to this reviewer")
	}

	if task.CurrentStatus != string(models.StatusInReview) {
		return nil, fmt.Errorf("task is not in review status: %s", task.CurrentStatus)
	}

	if hms.redis != nil {
		lockKey := fmt.Sprintf("task_review:%s", taskID)
		lockHolder, err := hms.redis.Get(ctx, lockKey).Result()
		if err == nil && lockHolder != reviewerID.String() {
			return nil, fmt.Errorf("task is locked by another reviewer")
		}
	}

	var video models.Video
	if err := hms.db.First(&video, task.VideoID).Error; err != nil {
		return nil, err
	}

	var autoResult *models.AutoModerationResult
	var ar models.AutoModerationResult
	if err := hms.db.Where("task_id = ?", taskID).First(&ar).Error; err == nil {
		autoResult = &ar
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return &ReviewContext{
		TaskID:     taskID,
		ReviewerID: reviewerID,
		Task:       &task,
		Video:      &video,
		AutoResult: autoResult,
	}, nil
}

func (hms *HumanModerationServiceV2) validateDecision(decision string) error {
	if decision != "approve" && decision != "reject" {
		return fmt.Errorf("invalid decision: %s (must be 'approve' or 'reject')", decision)
	}
	return nil
}

func (hms *HumanModerationServiceV2) createModerationLog(
	tx *gorm.DB,
	task *models.ModerationTask,
	action string,
	fromStatus string,
	toStatus string,
	actorType string,
	actorID *uuid.UUID,
	details map[string]interface{},
) error {
	log := &models.ModerationLog{
		TaskID:     task.ID,
		VideoID:    task.VideoID,
		ActorType:  actorType,
		ActorID:    actorID,
		Action:     action,
		FromStatus: fromStatus,
		ToStatus:   toStatus,
		Details:    details,
	}
	return tx.Create(log).Error
}

func (hms *HumanModerationServiceV2) updateReviewerPerformanceTx(
	tx *gorm.DB,
	reviewerID uuid.UUID,
	decision string,
) error {
	today := time.Now().Truncate(24 * time.Hour)

	var perf models.ReviewerPerformance
	err := tx.Where("reviewer_id = ? AND date = ?", reviewerID, today).
		First(&perf).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			perf = models.ReviewerPerformance{
				ReviewerID:  reviewerID,
				Date:        today,
				TotalReviews: 1,
			}
			if decision == "approve" {
				perf.ApprovedCount = 1
			} else {
				perf.RejectedCount = 1
			}
			return tx.Create(&perf).Error
		}
		return err
	}

	updates := map[string]interface{}{
		"total_reviews": perf.TotalReviews + 1,
	}
	if decision == "approve" {
		updates["approved_count"] = perf.ApprovedCount + 1
	} else {
		updates["rejected_count"] = perf.RejectedCount + 1
	}

	return tx.Model(&models.ReviewerPerformance{}).
		Where("id = ?", perf.ID).
		Updates(updates).Error
}

func (hms *HumanModerationServiceV2) updateDailyStatisticsTx(
	tx *gorm.DB,
	decision string,
	reviewDuration float64,
) error {
	today := time.Now().Truncate(24 * time.Hour)

	var stats models.DailyStatistics
	err := tx.Where("date = ?", today).First(&stats).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			stats = models.DailyStatistics{
				Date:              today,
				HumanReviewed:     1,
				AverageReviewTime: reviewDuration,
			}
			if decision == "approve" {
				stats.HumanApproved = 1
			} else {
				stats.HumanRejected = 1
			}
			return tx.Create(&stats).Error
		}
		return err
	}

	newHumanReviewed := stats.HumanReviewed + 1
	newAverage := (stats.AverageReviewTime*float64(stats.HumanReviewed) + reviewDuration) / float64(newHumanReviewed)

	updates := map[string]interface{}{
		"human_reviewed":     newHumanReviewed,
		"average_review_time": newAverage,
	}
	if decision == "approve" {
		updates["human_approved"] = stats.HumanApproved + 1
	} else {
		updates["human_rejected"] = stats.HumanRejected + 1
	}

	return tx.Model(&models.DailyStatistics{}).
		Where("id = ?", stats.ID).
		Updates(updates).Error
}

func (hms *HumanModerationServiceV2) ReleaseReviewLock(
	ctx context.Context,
	taskID uuid.UUID,
	reviewerID uuid.UUID,
) error {
	if hms.redis == nil {
		return nil
	}

	lockKey := fmt.Sprintf("task_review:%s", taskID)
	lockHolder, err := hms.redis.Get(ctx, lockKey).Result()
	
	if err != nil && err != redis.Nil {
		return err
	}

	if err == redis.Nil {
		return nil
	}

	if lockHolder != reviewerID.String() {
		return fmt.Errorf("lock is held by another user")
	}

	return hms.redis.Del(ctx, lockKey).Err()
}

func (hms *HumanModerationServiceV2) GetTaskSnapshotHistory(
	ctx context.Context,
	taskID uuid.UUID,
	limit int,
) ([]models.StateSnapshot, error) {
	return hms.txManager.GetSnapshotHistory(ctx, taskID, limit)
}

func (hms *HumanModerationServiceV2) CanRollbackTask(
	ctx context.Context,
	taskID uuid.UUID,
) (bool, string, error) {
	return hms.txManager.CanRollback(ctx, taskID)
}

func (hms *HumanModerationServiceV2) RollbackToLatestSnapshot(
	ctx context.Context,
	taskID uuid.UUID,
	actorID uuid.UUID,
	reason string,
) error {
	var task models.ModerationTask
	if err := hms.db.First(&task, taskID).Error; err != nil {
		return err
	}

	snapshot, err := hms.txManager.GetLatestSnapshot(ctx, taskID)
	if err != nil {
		return err
	}

	if snapshot.IsRolledBack {
		return errors.New("snapshot has already been rolled back")
	}

	err = hms.db.Transaction(func(tx *gorm.DB) error {
		if err := hms.txManager.RollbackSnapshot(ctx, snapshot); err != nil {
			return err
		}

		if snapshot.NewStatus == string(models.StatusHumanApproved) ||
			snapshot.NewStatus == string(models.StatusHumanRejected) {
			if err := tx.Where("task_id = ?", taskID).
				Delete(&models.HumanModerationResult{}).Error; err != nil {
				return err
			}

			if err := tx.Model(&models.ModerationTask{}).
				Where("id = ?", taskID).
				Updates(map[string]interface{}{
					"completed_at": nil,
				}).Error; err != nil {
				return err
			}
		}

		if snapshot.NewStatus == string(models.StatusHumanApproved) {
			if err := tx.Model(&models.Video{}).
				Where("id = ?", task.VideoID).
				Updates(map[string]interface{}{
					"status":       string(models.StatusInReview),
					"is_published": false,
				}).Error; err != nil {
				return err
			}
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

	if err != nil {
		return err
	}

	_, _, err = hms.txManager.VerifyStateConsistency(ctx, taskID)
	return err
}
