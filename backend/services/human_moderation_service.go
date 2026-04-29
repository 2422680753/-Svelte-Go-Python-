package services

import (
	"content-moderation/config"
	"content-moderation/models"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type HumanModerationService struct {
	db           *gorm.DB
	redis        *redis.Client
	config       *config.Config
	stateMachine *models.StateMachine
}

type ReviewDecision struct {
	Decision      string                 `json:"decision"`
	ViolationTags []string               `json:"violation_tags"`
	Comment       string                 `json:"comment"`
}

func NewHumanModerationService(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *HumanModerationService {
	return &HumanModerationService{
		db:           db,
		redis:        redis,
		config:       cfg,
		stateMachine: sm,
	}
}

func (hms *HumanModerationService) StartReview(taskID uuid.UUID, reviewerID uuid.UUID) error {
	var task models.ModerationTask
	if err := hms.db.Preload("Assignee").First(&task, taskID).Error; err != nil {
		return err
	}

	if task.AssignedTo == nil || *task.AssignedTo != reviewerID {
		return gorm.ErrRecordNotFound
	}

	if task.CurrentStatus != string(models.StatusAssigned) {
		return gorm.ErrInvalidTransaction
	}

	data := map[string]interface{}{}
	if err := hms.stateMachine.Transition(&task, models.StatusInReview, data, &reviewerID, "human"); err != nil {
		return err
	}

	return nil
}

func (hms *HumanModerationService) SubmitReview(taskID uuid.UUID, reviewerID uuid.UUID, decision ReviewDecision, reviewDuration float64) error {
	var task models.ModerationTask
	if err := hms.db.Preload("Assignee").First(&task, taskID).Error; err != nil {
		return err
	}

	if task.AssignedTo == nil || *task.AssignedTo != reviewerID {
		return gorm.ErrRecordNotFound
	}

	if task.CurrentStatus != string(models.StatusInReview) {
		return gorm.ErrInvalidTransaction
	}

	var nextStatus models.ModerationStatus
	switch decision.Decision {
	case "approve":
		nextStatus = models.StatusHumanApproved
	case "reject":
		nextStatus = models.StatusHumanRejected
	default:
		return gorm.ErrInvalidTransaction
	}

	data := map[string]interface{}{
		"decision":       decision.Decision,
		"violation_tags": decision.ViolationTags,
		"comment":        decision.Comment,
	}

	if err := hms.stateMachine.Transition(&task, nextStatus, data, &reviewerID, "human"); err != nil {
		return err
	}

	now := time.Now()
	task.HumanReviewAt = &now
	task.CompletedAt = &now
	hms.db.Save(&task)

	result := &models.HumanModerationResult{
		TaskID:         taskID,
		ReviewerID:     reviewerID,
		Decision:       decision.Decision,
		ViolationTags:  decision.ViolationTags,
		Comment:        decision.Comment,
		ReviewDuration: reviewDuration,
		IsAppeal:       false,
	}

	if err := hms.db.Create(result).Error; err != nil {
		return err
	}

	hms.updateReviewerPerformance(reviewerID, decision.Decision)
	hms.updateDailyStatistics(decision.Decision, reviewDuration)

	return nil
}

func (hms *HumanModerationService) updateReviewerPerformance(reviewerID uuid.UUID, decision string) {
	today := time.Now().Truncate(24 * time.Hour)
	
	var perf models.ReviewerPerformance
	if err := hms.db.Where("reviewer_id = ? AND date = ?", reviewerID, today).First(&perf).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			perf = models.ReviewerPerformance{
				ReviewerID: reviewerID,
				Date:       today,
			}
			hms.db.Create(&perf)
		}
	}

	perf.TotalReviews++
	if decision == "approve" {
		perf.ApprovedCount++
	} else if decision == "reject" {
		perf.RejectedCount++
	}

	hms.db.Save(&perf)
}

func (hms *HumanModerationService) updateDailyStatistics(decision string, reviewDuration float64) {
	today := time.Now().Truncate(24 * time.Hour)
	
	var stats models.DailyStatistics
	if err := hms.db.Where("date = ?", today).First(&stats).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			stats = models.DailyStatistics{
				Date: today,
			}
			hms.db.Create(&stats)
		}
	}

	stats.HumanReviewed++
	if decision == "approve" {
		stats.HumanApproved++
	} else if decision == "reject" {
		stats.HumanRejected++
	}

	if stats.AverageReviewTime == 0 {
		stats.AverageReviewTime = reviewDuration
	} else {
		stats.AverageReviewTime = (stats.AverageReviewTime*float64(stats.HumanReviewed-1) + reviewDuration) / float64(stats.HumanReviewed)
	}

	hms.db.Save(&stats)
}

func (hms *HumanModerationService) GetPendingTasks(reviewerID uuid.UUID, limit int) ([]models.ModerationTask, error) {
	var tasks []models.ModerationTask
	query := hms.db.Preload("Video").Where("assigned_to = ? AND current_status IN ?", 
		reviewerID, []string{string(models.StatusAssigned), string(models.StatusInReview)})
	
	if limit > 0 {
		query = query.Limit(limit)
	}

	if err := query.Order("CASE priority WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, sla_deadline ASC").
		Find(&tasks).Error; err != nil {
		return nil, err
	}

	return tasks, nil
}

func (hms *HumanModerationService) GetTaskForReview(taskID uuid.UUID, reviewerID uuid.UUID) (*models.ModerationTask, []models.VideoFrame, *models.AutoModerationResult, error) {
	var task models.ModerationTask
	if err := hms.db.Preload("Video").Preload("Assignee").First(&task, taskID).Error; err != nil {
		return nil, nil, nil, err
	}

	if task.AssignedTo == nil || *task.AssignedTo != reviewerID {
		return nil, nil, nil, gorm.ErrRecordNotFound
	}

	frameService := NewVideoFrameService(hms.db, hms.redis, hms.config)
	frames, err := frameService.GetVideoFrames(task.VideoID, false)
	if err != nil {
		return nil, nil, nil, err
	}

	var autoResult *models.AutoModerationResult
	if err := hms.db.Where("task_id = ?", taskID).First(&autoResult).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, nil, nil, err
		}
		autoResult = nil
	}

	return &task, frames, autoResult, nil
}

func (hms *HumanModerationService) GetReviewHistory(reviewerID uuid.UUID, page, pageSize int) ([]models.HumanModerationResult, int64, error) {
	var results []models.HumanModerationResult
	var total int64

	query := hms.db.Model(&models.HumanModerationResult{}).Where("reviewer_id = ?", reviewerID)
	query.Count(&total)

	offset := (page - 1) * pageSize
	if err := hms.db.Preload("Task.Video").Where("reviewer_id = ?", reviewerID).
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&results).Error; err != nil {
		return nil, 0, err
	}

	return results, total, nil
}
