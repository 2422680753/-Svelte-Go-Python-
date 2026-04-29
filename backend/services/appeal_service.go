package services

import (
	"content-moderation/config"
	"content-moderation/models"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AppealService struct {
	db           *gorm.DB
	redis        *redis.Client
	config       *config.Config
	stateMachine *models.StateMachine
}

func NewAppealService(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *AppealService {
	return &AppealService{
		db:           db,
		redis:        redis,
		config:       cfg,
		stateMachine: sm,
	}
}

func (as *AppealService) SubmitAppeal(taskID uuid.UUID, submitterID uuid.UUID, reason string) (*models.Appeal, error) {
	var task models.ModerationTask
	if err := as.db.Preload("Video").First(&task, taskID).Error; err != nil {
		return nil, err
	}

	if task.CurrentStatus != string(models.StatusAutoRejected) && 
	   task.CurrentStatus != string(models.StatusHumanRejected) &&
	   task.CurrentStatus != string(models.StatusBanned) {
		return nil, fmt.Errorf("cannot appeal task with status: %s", task.CurrentStatus)
	}

	var existingAppeal models.Appeal
	if err := as.db.Where("task_id = ? AND status IN ?", taskID, []string{"pending", "in_review"}).First(&existingAppeal).Error; err == nil {
		return nil, fmt.Errorf("there is already an active appeal for this task")
	}

	appeal := &models.Appeal{
		TaskID:          taskID,
		VideoID:         task.VideoID,
		OriginalDecision: task.CurrentStatus,
		Reason:          reason,
		SubmittedBy:     submitterID,
		Status:          string(models.StatusAppealed),
	}

	if err := as.db.Create(appeal).Error; err != nil {
		return nil, err
	}

	data := map[string]interface{}{
		"appeal_reason": reason,
		"appeal_id":     appeal.ID,
	}
	if err := as.stateMachine.Transition(&task, models.StatusAppealed, data, &submitterID, "human"); err != nil {
		return nil, err
	}

	as.notifyAdminsOfAppeal(appeal)

	return appeal, nil
}

func (as *AppealService) GetPendingAppeals() ([]models.Appeal, error) {
	var appeals []models.Appeal
	if err := as.db.Preload("Task.Video").Preload("Submitter").
		Where("status = ?", string(models.StatusAppealed)).
		Order("created_at ASC").
		Find(&appeals).Error; err != nil {
		return nil, err
	}
	return appeals, nil
}

func (as *AppealService) AssignAppeal(appealID uuid.UUID, reviewerID uuid.UUID) error {
	var appeal models.Appeal
	if err := as.db.First(&appeal, appealID).Error; err != nil {
		return err
	}

	if appeal.Status != string(models.StatusAppealed) {
		return fmt.Errorf("cannot assign appeal with status: %s", appeal.Status)
	}

	var reviewer models.User
	if err := as.db.First(&reviewer, reviewerID).Error; err != nil {
		return err
	}

	if reviewer.Role != "admin" && reviewer.Role != "senior_reviewer" {
		return fmt.Errorf("only admins and senior reviewers can handle appeals")
	}

	appeal.AssignedTo = &reviewerID
	appeal.Status = "in_review"

	if err := as.db.Save(&appeal).Error; err != nil {
		return err
	}

	var task models.ModerationTask
	if err := as.db.First(&task, appeal.TaskID).Error; err == nil {
		data := map[string]interface{}{
			"assigned_to": reviewerID.String(),
		}
		as.stateMachine.Transition(&task, models.StatusAppealed, data, &reviewerID, "human")
	}

	return nil
}

func (as *AppealService) ResolveAppeal(appealID uuid.UUID, reviewerID uuid.UUID, result string, comment string) error {
	var appeal models.Appeal
	if err := as.db.Preload("Task.Video").First(&appeal, appealID).Error; err != nil {
		return err
	}

	if appeal.Status != "in_review" && appeal.Status != string(models.StatusAppealed) {
		return fmt.Errorf("cannot resolve appeal with status: %s", appeal.Status)
	}

	var task models.ModerationTask
	if err := as.db.First(&task, appeal.TaskID).Error; err != nil {
		return err
	}

	now := time.Now()
	appeal.ResolvedAt = &now
	appeal.AppealResult = result
	appeal.AppealComment = comment

	data := map[string]interface{}{
		"appeal_result":  result,
		"appeal_comment": comment,
	}

	if result == "approve" {
		appeal.Status = string(models.StatusAppealApproved)
		if err := as.stateMachine.Transition(&task, models.StatusAppealApproved, data, &reviewerID, "human"); err != nil {
			return err
		}

		result := &models.HumanModerationResult{
			TaskID:         task.ID,
			ReviewerID:     reviewerID,
			Decision:       "approve",
			ViolationTags:  []string{},
			Comment:        "Appeal approved",
			ReviewDuration: 0,
			IsAppeal:       true,
			AppealID:       &appeal.ID,
		}
		as.db.Create(result)

		if err := as.stateMachine.Transition(&task, models.StatusNeedReview, data, &reviewerID, "human"); err != nil {
			return err
		}

		as.updateReviewerPerformanceForAppeal(appeal.SubmittedBy, true)
	} else {
		appeal.Status = string(models.StatusAppealRejected)
		if err := as.stateMachine.Transition(&task, models.StatusAppealRejected, data, &reviewerID, "human"); err != nil {
			return err
		}

		result := &models.HumanModerationResult{
			TaskID:         task.ID,
			ReviewerID:     reviewerID,
			Decision:       "reject",
			ViolationTags:  []string{},
			Comment:        "Appeal rejected: " + comment,
			ReviewDuration: 0,
			IsAppeal:       true,
			AppealID:       &appeal.ID,
		}
		as.db.Create(result)

		if err := as.stateMachine.Transition(&task, models.StatusBanned, data, &reviewerID, "human"); err != nil {
			return err
		}

		as.updateReviewerPerformanceForAppeal(appeal.SubmittedBy, false)
	}

	if err := as.db.Save(&appeal).Error; err != nil {
		return err
	}

	as.updateAppealStatistics(result)

	return nil
}

func (as *AppealService) updateReviewerPerformanceForAppeal(reviewerID uuid.UUID, isOverturned bool) {
	today := time.Now().Truncate(24 * time.Hour)
	
	var perf models.ReviewerPerformance
	if err := as.db.Where("reviewer_id = ? AND date = ?", reviewerID, today).First(&perf).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return
		}
	}

	if isOverturned {
		perf.AppealsOverturnCount++
	} else {
		perf.AppealsUpholdCount++
	}

	totalAppeals := perf.AppealsOverturnCount + perf.AppealsUpholdCount
	if totalAppeals > 0 {
		perf.AccuracyScore = float64(perf.AppealsUpholdCount) / float64(totalAppeals)
	}

	as.db.Save(&perf)
}

func (as *AppealService) updateAppealStatistics(result string) {
	today := time.Now().Truncate(24 * time.Hour)
	
	var stats models.DailyStatistics
	if err := as.db.Where("date = ?", today).First(&stats).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return
		}
	}

	stats.AppealsResolved++
	as.db.Save(&stats)
}

func (as *AppealService) notifyAdminsOfAppeal(appeal *models.Appeal) {
	var admins []models.User
	if err := as.db.Where("role = ?", "admin").Find(&admins).Error; err != nil {
		return
	}

	ctx := as.redis.Context()
	notification := map[string]interface{}{
		"type":        "new_appeal",
		"appeal_id":   appeal.ID.String(),
		"video_id":    appeal.VideoID.String(),
		"task_id":     appeal.TaskID.String(),
		"reason":      appeal.Reason,
		"submitted_by": appeal.SubmittedBy.String(),
		"timestamp":   time.Now().Unix(),
	}

	notificationData, _ := as.db.Dialector.(interface{}).BindVarTo(notification, 0)
	_ = notificationData

	for _, admin := range admins {
		channel := fmt.Sprintf("user:%s:notifications", admin.ID)
		as.redis.Publish(ctx, channel, "New appeal submitted")
	}
}

func (as *AppealService) GetAppealDetails(appealID uuid.UUID) (*models.Appeal, *models.ModerationTask, []models.VideoFrame, *models.AutoModerationResult, error) {
	var appeal models.Appeal
	if err := as.db.Preload("Task.Video").Preload("Submitter").First(&appeal, appealID).Error; err != nil {
		return nil, nil, nil, nil, err
	}

	var task models.ModerationTask
	if err := as.db.Preload("Video").First(&task, appeal.TaskID).Error; err != nil {
		return nil, nil, nil, nil, err
	}

	frameService := NewVideoFrameService(as.db, as.redis, as.config)
	frames, err := frameService.GetVideoFrames(task.VideoID, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	var autoResult *models.AutoModerationResult
	if err := as.db.Where("task_id = ?", task.ID).First(&autoResult).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, nil, nil, nil, err
		}
	}

	return &appeal, &task, frames, autoResult, nil
}
