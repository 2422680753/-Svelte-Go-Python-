package services

import (
	"content-moderation/config"
	"content-moderation/models"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TaskDistributor struct {
	db         *gorm.DB
	redis      *redis.Client
	config     *config.Config
	stateMachine *models.StateMachine
}

type TaskQueue struct {
	HighPriority   []string
	NormalPriority []string
	LowPriority    []string
}

func NewTaskDistributor(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *TaskDistributor {
	return &TaskDistributor{
		db:          db,
		redis:       redis,
		config:      cfg,
		stateMachine: sm,
	}
}

func (td *TaskDistributor) CreateTask(videoID uuid.UUID, priority string) (*models.ModerationTask, error) {
	task := &models.ModerationTask{
		VideoID:       videoID,
		CurrentStatus: string(models.StatusPending),
		Priority:      priority,
		SLADeadline:   models.CalculateSLADeadline(td.config),
	}

	if err := td.db.Create(task).Error; err != nil {
		return nil, err
	}

	if err := td.EnqueueTask(task); err != nil {
		log.Printf("Failed to enqueue task %s: %v", task.ID, err)
	}

	return task, nil
}

func (td *TaskDistributor) EnqueueTask(task *models.ModerationTask) error {
	ctx := td.redis.Context()
	queueKey := td.getQueueKey(task.Priority)

	taskData, err := json.Marshal(map[string]interface{}{
		"task_id":    task.ID,
		"video_id":   task.VideoID,
		"priority":   task.Priority,
		"created_at": task.CreatedAt,
	})
	if err != nil {
		return err
	}

	if err := td.redis.LPush(ctx, queueKey, taskData).Err(); err != nil {
		return err
	}

	return td.redis.Publish(ctx, "task_queue_update", task.Priority).Err()
}

func (td *TaskDistributor) DequeueTask(priority string) (*models.ModerationTask, error) {
	ctx := td.redis.Context()
	queueKey := td.getQueueKey(priority)

	result, err := td.redis.RPop(ctx, queueKey).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(result), &taskData); err != nil {
		return nil, err
	}

	taskIDStr, ok := taskData["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id format")
	}

	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return nil, err
	}

	var task models.ModerationTask
	if err := td.db.Preload("Video").First(&task, taskID).Error; err != nil {
		return nil, err
	}

	return &task, nil
}

func (td *TaskDistributor) getQueueKey(priority string) string {
	switch priority {
	case "high":
		return "task_queue:high"
	case "low":
		return "task_queue:low"
	default:
		return "task_queue:normal"
	}
}

func (td *TaskDistributor) GetNextTask(priorities []string) (*models.ModerationTask, error) {
	for _, priority := range priorities {
		task, err := td.DequeueTask(priority)
		if err != nil {
			continue
		}
		if task != nil {
			return task, nil
		}
	}
	return nil, nil
}

func (td *TaskDistributor) AssignTask(taskID uuid.UUID, reviewerID uuid.UUID) error {
	var task models.ModerationTask
	if err := td.db.First(&task, taskID).Error; err != nil {
		return err
	}

	var reviewer models.User
	if err := td.db.First(&reviewer, reviewerID).Error; err != nil {
		return err
	}

	if reviewer.Role != "reviewer" && reviewer.Role != "admin" {
		return fmt.Errorf("user is not a reviewer or admin")
	}

	task.AssignedTo = &reviewerID
	if err := td.db.Save(&task).Error; err != nil {
		return err
	}

	data := map[string]interface{}{
		"reviewer_id": reviewerID.String(),
	}
	if err := td.stateMachine.Transition(&task, models.StatusAssigned, data, &reviewerID, "human"); err != nil {
		return err
	}

	td.notifyReviewer(reviewerID, task.ID)
	return nil
}

func (td *TaskDistributor) notifyReviewer(reviewerID uuid.UUID, taskID uuid.UUID) {
	ctx := td.redis.Context()
	notification := map[string]interface{}{
		"reviewer_id": reviewerID.String(),
		"task_id":     taskID.String(),
		"type":        "task_assigned",
		"timestamp":   time.Now().Unix(),
	}

	notificationData, _ := json.Marshal(notification)
	td.redis.Publish(ctx, fmt.Sprintf("reviewer:%s:notifications", reviewerID), notificationData)
}

func (td *TaskDistributor) AutoAssignTasks() error {
	var availableReviewers []models.User
	if err := td.db.Where("role IN ? AND is_active = ?", []string{"reviewer", "admin"}, true).Find(&availableReviewers).Error; err != nil {
		return err
	}

	if len(availableReviewers) == 0 {
		return nil
	}

	var tasks []models.ModerationTask
	if err := td.db.Where("current_status = ? AND assigned_to IS NULL", models.StatusNeedReview).
		Order("CASE priority WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, sla_deadline ASC").
		Find(&tasks).Error; err != nil {
		return err
	}

	for i, task := range tasks {
		reviewerIndex := i % len(availableReviewers)
		reviewer := availableReviewers[reviewerIndex]
		if err := td.AssignTask(task.ID, reviewer.ID); err != nil {
			log.Printf("Failed to assign task %s to reviewer %s: %v", task.ID, reviewer.ID, err)
			continue
		}
	}

	return nil
}

func (td *TaskDistributor) GetReviewerWorkload(reviewerID uuid.UUID) (map[string]interface{}, error) {
	var pendingCount int64
	if err := td.db.Model(&models.ModerationTask{}).
		Where("assigned_to = ? AND current_status IN ?", reviewerID, []string{
			string(models.StatusAssigned),
			string(models.StatusInReview),
		}).
		Count(&pendingCount).Error; err != nil {
		return nil, err
	}

	var completedToday int64
	today := time.Now().Truncate(24 * time.Hour)
	if err := td.db.Model(&models.HumanModerationResult{}).
		Where("reviewer_id = ? AND created_at >= ?", reviewerID, today).
		Count(&completedToday).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"pending_count":   pendingCount,
		"completed_today": completedToday,
	}, nil
}
