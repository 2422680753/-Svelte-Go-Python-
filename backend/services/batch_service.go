package services

import (
	"content-moderation/config"
	"content-moderation/models"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BatchService struct {
	db           *gorm.DB
	redis        *redis.Client
	config       *config.Config
	stateMachine *models.StateMachine
}

type BatchOperationRequest struct {
	TaskIDs       []uuid.UUID          `json:"task_ids"`
	OperationType string                 `json:"operation_type"`
	Parameters    map[string]interface{} `json:"parameters"`
}

type BatchOperationResult struct {
	SuccessCount int
	FailedCount  int
	ErrorDetails []map[string]interface{}
}

func NewBatchService(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *BatchService {
	return &BatchService{
		db:           db,
		redis:        redis,
		config:       cfg,
		stateMachine: sm,
	}
}

func (bs *BatchService) CreateBatchOperation(operatorID uuid.UUID, request BatchOperationRequest) (*models.BatchOperation, error) {
	taskIDsJSON, err := json.Marshal(request.TaskIDs)
	if err != nil {
		return nil, err
	}

	paramsJSON, err := json.Marshal(request.Parameters)
	if err != nil {
		return nil, err
	}

	operation := &models.BatchOperation{
		OperatorID:    operatorID,
		OperationType: request.OperationType,
		TaskIDs:       taskIDsJSON,
		TotalCount:    len(request.TaskIDs),
		Status:        "processing",
		Parameters:    paramsJSON,
	}

	if err := bs.db.Create(operation).Error; err != nil {
		return nil, err
	}

	go bs.processBatchOperationAsync(operation, request)

	return operation, nil
}

func (bs *BatchService) processBatchOperationAsync(operation *models.BatchOperation, request BatchOperationRequest) {
	var wg sync.WaitGroup
	var mutex sync.Mutex
	
	successCount := 0
	failedCount := 0
	errorDetails := []map[string]interface{}{}

	maxWorkers := 10
	semaphore := make(chan struct{}, maxWorkers)

	for _, taskID := range request.TaskIDs {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(id uuid.UUID) {
			defer wg.Done()
			defer func() { <-semaphore }()

			var err error
			switch request.OperationType {
			case "batch_approve":
				err = bs.batchApprove(id, operation.OperatorID)
			case "batch_reject":
				err = bs.batchReject(id, operation.OperatorID, request.Parameters)
			case "batch_assign":
				err = bs.batchAssign(id, request.Parameters)
			case "batch_publish":
				err = bs.batchPublish(id, operation.OperatorID)
			case "batch_ban":
				err = bs.batchBan(id, operation.OperatorID)
			default:
				err = fmt.Errorf("unknown operation type: %s", request.OperationType)
			}

			mutex.Lock()
			defer mutex.Unlock()

			if err != nil {
				failedCount++
				errorDetails = append(errorDetails, map[string]interface{}{
					"task_id": id.String(),
					"error":   err.Error(),
				})
			} else {
				successCount++
			}
		}(taskID)
	}

	wg.Wait()

	operation.SuccessCount = successCount
	operation.FailedCount = failedCount
	operation.Status = "completed"

	if len(errorDetails) > 0 {
		errorJSON, _ := json.Marshal(errorDetails)
		operation.ErrorDetails = errorJSON
	}

	bs.db.Save(operation)
	log.Printf("Batch operation %s completed: %d success, %d failed", 
		operation.ID, successCount, failedCount)
}

func (bs *BatchService) batchApprove(taskID uuid.UUID, operatorID uuid.UUID) error {
	var task models.ModerationTask
	if err := bs.db.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.CurrentStatus != string(models.StatusInReview) {
		return fmt.Errorf("task %s is not in review status", taskID)
	}

	data := map[string]interface{}{
		"decision": "approve",
		"batch":    true,
	}

	if err := bs.stateMachine.Transition(&task, models.StatusHumanApproved, data, &operatorID, "human"); err != nil {
		return err
	}

	now := time.Now()
	task.HumanReviewAt = &now
	task.CompletedAt = &now
	bs.db.Save(&task)

	result := &models.HumanModerationResult{
		TaskID:         taskID,
		ReviewerID:     operatorID,
		Decision:       "approve",
		ViolationTags:  []string{},
		Comment:        "Batch approved",
		ReviewDuration: 0,
		IsAppeal:       false,
	}

	return bs.db.Create(result).Error
}

func (bs *BatchService) batchReject(taskID uuid.UUID, operatorID uuid.UUID, params map[string]interface{}) error {
	var task models.ModerationTask
	if err := bs.db.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.CurrentStatus != string(models.StatusInReview) {
		return fmt.Errorf("task %s is not in review status", taskID)
	}

	violationTags, _ := params["violation_tags"].([]string)
	comment, _ := params["comment"].(string)

	data := map[string]interface{}{
		"decision":       "reject",
		"violation_tags": violationTags,
		"comment":        comment,
		"batch":          true,
	}

	if err := bs.stateMachine.Transition(&task, models.StatusHumanRejected, data, &operatorID, "human"); err != nil {
		return err
	}

	now := time.Now()
	task.HumanReviewAt = &now
	task.CompletedAt = &now
	bs.db.Save(&task)

	result := &models.HumanModerationResult{
		TaskID:         taskID,
		ReviewerID:     operatorID,
		Decision:       "reject",
		ViolationTags:  violationTags,
		Comment:        comment,
		ReviewDuration: 0,
		IsAppeal:       false,
	}

	return bs.db.Create(result).Error
}

func (bs *BatchService) batchAssign(taskID uuid.UUID, params map[string]interface{}) error {
	reviewerIDStr, ok := params["reviewer_id"].(string)
	if !ok {
		return fmt.Errorf("reviewer_id not provided")
	}

	reviewerID, err := uuid.Parse(reviewerIDStr)
	if err != nil {
		return err
	}

	taskDistributor := NewTaskDistributor(bs.db, bs.redis, bs.config, bs.stateMachine)
	return taskDistributor.AssignTask(taskID, reviewerID)
}

func (bs *BatchService) batchPublish(taskID uuid.UUID, operatorID uuid.UUID) error {
	var task models.ModerationTask
	if err := bs.db.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.CurrentStatus != string(models.StatusAutoApproved) && 
	   task.CurrentStatus != string(models.StatusHumanApproved) {
		return fmt.Errorf("task %s is not approved", taskID)
	}

	data := map[string]interface{}{
		"batch": true,
	}

	return bs.stateMachine.Transition(&task, models.StatusPublished, data, &operatorID, "human")
}

func (bs *BatchService) batchBan(taskID uuid.UUID, operatorID uuid.UUID) error {
	var task models.ModerationTask
	if err := bs.db.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.CurrentStatus != string(models.StatusAutoRejected) && 
	   task.CurrentStatus != string(models.StatusHumanRejected) &&
	   task.CurrentStatus != string(models.StatusAppealRejected) {
		return fmt.Errorf("task %s is not rejected", taskID)
	}

	data := map[string]interface{}{
		"batch": true,
	}

	return bs.stateMachine.Transition(&task, models.StatusBanned, data, &operatorID, "human")
}

func (bs *BatchService) GetBatchOperationStatus(operationID uuid.UUID) (*models.BatchOperation, error) {
	var operation models.BatchOperation
	if err := bs.db.Preload("Operator").First(&operation, operationID).Error; err != nil {
		return nil, err
	}
	return &operation, nil
}

func (bs *BatchService) GetBatchOperations(operatorID uuid.UUID, page, pageSize int) ([]models.BatchOperation, int64, error) {
	var operations []models.BatchOperation
	var total int64

	query := bs.db.Model(&models.BatchOperation{})
	if operatorID != uuid.Nil {
		query = query.Where("operator_id = ?", operatorID)
	}

	query.Count(&total)

	offset := (page - 1) * pageSize
	if err := bs.db.Preload("Operator").
		Where("operator_id = ?", operatorID).
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&operations).Error; err != nil {
		return nil, 0, err
	}

	return operations, total, nil
}

func (bs *BatchService) ValidateBatchOperation(request BatchOperationRequest, operatorID uuid.UUID) (bool, []string) {
	warnings := []string{}

	if len(request.TaskIDs) == 0 {
		warnings = append(warnings, "No tasks specified")
		return false, warnings
	}

	if len(request.TaskIDs) > 1000 {
		warnings = append(warnings, "Batch size exceeds recommended limit of 1000")
	}

	validOperations := map[string]bool{
		"batch_approve": true,
		"batch_reject":  true,
		"batch_assign":  true,
		"batch_publish": true,
		"batch_ban":     true,
	}

	if !validOperations[request.OperationType] {
		warnings = append(warnings, fmt.Sprintf("Invalid operation type: %s", request.OperationType))
		return false, warnings
	}

	if request.OperationType == "batch_assign" {
		if _, ok := request.Parameters["reviewer_id"]; !ok {
			warnings = append(warnings, "reviewer_id is required for batch_assign")
			return false, warnings
		}
	}

	var tasks []models.ModerationTask
	if err := bs.db.Where("id IN ?", request.TaskIDs).Find(&tasks).Error; err != nil {
		warnings = append(warnings, "Failed to validate tasks")
		return false, warnings
	}

	if len(tasks) != len(request.TaskIDs) {
		warnings = append(warnings, "Some tasks were not found")
	}

	switch request.OperationType {
	case "batch_approve", "batch_reject":
		for _, task := range tasks {
			if task.CurrentStatus != string(models.StatusInReview) {
				warnings = append(warnings, fmt.Sprintf("Task %s is not in review status", task.ID))
			}
		}
	case "batch_publish":
		for _, task := range tasks {
			if task.CurrentStatus != string(models.StatusAutoApproved) && 
			   task.CurrentStatus != string(models.StatusHumanApproved) {
				warnings = append(warnings, fmt.Sprintf("Task %s is not approved", task.ID))
			}
		}
	case "batch_ban":
		for _, task := range tasks {
			if task.CurrentStatus != string(models.StatusAutoRejected) && 
			   task.CurrentStatus != string(models.StatusHumanRejected) &&
			   task.CurrentStatus != string(models.StatusAppealRejected) {
				warnings = append(warnings, fmt.Sprintf("Task %s is not rejected", task.ID))
			}
		}
	}

	return len(warnings) == 0, warnings
}
