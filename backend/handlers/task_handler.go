package handlers

import (
	"content-moderation/config"
	"content-moderation/models"
	"content-moderation/services"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TaskHandler struct {
	db                      *gorm.DB
	redis                   *redis.Client
	config                  *config.Config
	stateMachine            *models.StateMachine
	txManager               *models.StateTransactionManager
	recoveryService         *services.StateRecoveryService
	taskDistributor         *services.TaskDistributor
	humanModerationService  *services.HumanModerationService
	humanModerationServiceV2 *services.HumanModerationServiceV2
	batchService            *services.BatchService
}

func NewTaskHandler(
	db *gorm.DB, 
	redis *redis.Client, 
	cfg *config.Config, 
	sm *models.StateMachine,
) *TaskHandler {
	txManager := models.NewStateTransactionManager(db, redis)
	recoveryService := services.NewStateRecoveryService(db, redis, cfg, txManager)
	
	return &TaskHandler{
		db:                      db,
		redis:                   redis,
		config:                  cfg,
		stateMachine:            sm,
		txManager:               txManager,
		recoveryService:         recoveryService,
		taskDistributor:         services.NewTaskDistributor(db, redis, cfg, sm),
		humanModerationService:  services.NewHumanModerationService(db, redis, cfg, sm),
		humanModerationServiceV2: services.NewHumanModerationServiceV2(db, redis, cfg, sm, txManager),
		batchService:            services.NewBatchService(db, redis, cfg, sm),
	}
}

type CreateVideoRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	VideoURL    string `json:"video_url" binding:"required"`
	Duration    int    `json:"duration"`
	Priority    string `json:"priority"`
}

func (h *TaskHandler) CreateVideoAndTask(c *gin.Context) {
	var req CreateVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	video := &models.Video{
		Title:       req.Title,
		Description: req.Description,
		VideoURL:    req.VideoURL,
		Duration:    req.Duration,
		UploaderID:  userID.(uuid.UUID),
		Status:      string(models.StatusPending),
	}

	if err := h.db.Create(video).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create video"})
		return
	}

	priority := req.Priority
	if priority == "" {
		priority = "normal"
	}

	task, err := h.taskDistributor.CreateTask(video.ID, priority)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create moderation task"})
		return
	}

	autoService := services.NewAutoModerationService(h.db, h.redis, h.config, h.stateMachine)
	go autoService.ProcessAutoModeration(task.ID)

	c.JSON(http.StatusCreated, gin.H{
		"video": video,
		"task":  task,
	})
}

func (h *TaskHandler) GetTasks(c *gin.Context) {
	status := c.Query("status")
	assignedTo := c.Query("assigned_to")
	priority := c.Query("priority")
	page := 1
	pageSize := 20

	if c.Query("page") != "" {
		fmt.Sscanf(c.Query("page"), "%d", &page)
	}
	if c.Query("page_size") != "" {
		fmt.Sscanf(c.Query("page_size"), "%d", &pageSize)
	}

	var tasks []models.ModerationTask
	var total int64

	query := h.db.Model(&models.ModerationTask{}).Preload("Video").Preload("Assignee")

	if status != "" {
		query = query.Where("current_status = ?", status)
	}
	if assignedTo != "" {
		assignedToUUID, err := uuid.Parse(assignedTo)
		if err == nil {
			query = query.Where("assigned_to = ?", assignedToUUID)
		}
	}
	if priority != "" {
		query = query.Where("priority = ?", priority)
	}

	query.Count(&total)

	offset := (page - 1) * pageSize
	if err := query.Order("CASE priority WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, sla_deadline ASC").
		Offset(offset).Limit(pageSize).
		Find(&tasks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get tasks"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tasks":       tasks,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
	})
}

func (h *TaskHandler) GetTask(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var task models.ModerationTask
	if err := h.db.Preload("Video").Preload("Assignee").First(&task, taskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
		return
	}

	frameService := services.NewVideoFrameService(h.db, h.redis, h.config)
	frames, err := frameService.GetVideoFrames(task.VideoID, false)
	if err != nil {
		frames = []models.VideoFrame{}
	}

	var autoResult *models.AutoModerationResult
	if err := h.db.Where("task_id = ?", taskID).First(&autoResult).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get auto moderation result"})
			return
		}
	}

	var humanResult *models.HumanModerationResult
	if err := h.db.Where("task_id = ?", taskID).Order("created_at DESC").First(&humanResult).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get human moderation result"})
			return
		}
	}

	logs := []models.ModerationLog{}
	if err := h.db.Preload("Actor").Where("task_id = ?", taskID).Order("created_at DESC").Find(&logs).Error; err != nil {
		logs = []models.ModerationLog{}
	}

	snapshotHistory, err := h.txManager.GetSnapshotHistory(c.Request.Context(), taskID, 10)
	if err != nil {
		snapshotHistory = []models.StateSnapshot{}
	}

	canRollback, rollbackReason, _ := h.txManager.CanRollback(c.Request.Context(), taskID)

	c.JSON(http.StatusOK, gin.H{
		"task":             task,
		"frames":           frames,
		"auto_result":      autoResult,
		"human_result":     humanResult,
		"moderation_logs":  logs,
		"snapshot_history": snapshotHistory,
		"can_rollback":     canRollback,
		"rollback_reason":  rollbackReason,
		"frame_status":     frameService.GetFrameExtractionStatus(task.VideoID),
	})
}

func (h *TaskHandler) GetFrameMapping(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var task models.ModerationTask
	if err := h.db.First(&task, taskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
		return
	}

	frameService := services.NewVideoFrameService(h.db, h.redis, h.config)
	ctx := c.Request.Context()
	
	mapping, err := frameService.GetFrameMapping(ctx, taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get frame mapping"})
		return
	}

	c.JSON(http.StatusOK, mapping)
}

func (h *TaskHandler) GetFramesPaginated(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	page := 1
	pageSize := 20

	if c.Query("page") != "" {
		fmt.Sscanf(c.Query("page"), "%d", &page)
	}
	if c.Query("page_size") != "" {
		fmt.Sscanf(c.Query("page_size"), "%d", &pageSize)
	}

	frameService := services.NewVideoFrameService(h.db, h.redis, h.config)
	ctx := c.Request.Context()

	frames, total, err := frameService.GetFramesPaginated(ctx, taskID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get frames"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"frames":      frames,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
	})
}

func (h *TaskHandler) GetFramesByTime(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	frameService := services.NewVideoFrameService(h.db, h.redis, h.config)
	ctx := c.Request.Context()

	timeStr := c.Query("time")
	startStr := c.Query("start")
	endStr := c.Query("end")

	if timeStr != "" {
		var timestamp float64
		fmt.Sscanf(timeStr, "%f", &timestamp)

		frame, err := frameService.FindFrameByTime(ctx, taskID, timestamp)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "Frame not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find frame"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"frame": frame,
		})
		return
	}

	if startStr != "" && endStr != "" {
		var startTime, endTime float64
		fmt.Sscanf(startStr, "%f", &startTime)
		fmt.Sscanf(endStr, "%f", &endTime)

		frames, err := frameService.GetVideoFramesForTask(ctx, taskID, uuid.Nil)
		if err != nil {
			frames = []models.VideoFrame{}
		}

		var filtered []models.VideoFrame
		for _, f := range frames {
			if f.Timestamp >= startTime && f.Timestamp <= endTime {
				filtered = append(filtered, f)
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"frames": filtered,
		})
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{"error": "Required query params: time OR (start AND end)"})
}

func (h *TaskHandler) GetFlaggedFrames(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	frameService := services.NewVideoFrameService(h.db, h.redis, h.config)
	ctx := c.Request.Context()

	frames, err := frameService.GetFlaggedFrames(ctx, taskID)
	if err != nil {
		frames = []models.VideoFrame{}
	}

	c.JSON(http.StatusOK, gin.H{
		"frames": frames,
	})
}

func (h *TaskHandler) GetFrameExtractionProgress(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	frameService := services.NewVideoFrameService(h.db, h.redis, h.config)
	status := frameService.GetFrameExtractionStatusForTask(taskID)

	c.JSON(http.StatusOK, gin.H{
		"task_id": taskID,
		"status":  status,
	})
}

func (h *TaskHandler) StartReview(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	if err := h.humanModerationServiceV2.StartReview(c.Request.Context(), taskID, userID.(uuid.UUID)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "START_REVIEW_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Review started",
		"task_id": taskID,
	})
}

func (h *TaskHandler) SubmitReview(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req struct {
		Decision       string   `json:"decision" binding:"required"`
		ViolationTags  []string `json:"violation_tags"`
		Comment        string   `json:"comment"`
		ReviewDuration float64  `json:"review_duration"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Decision != "approve" && req.Decision != "reject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid decision: must be 'approve' or 'reject'"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	reviewDuration := req.ReviewDuration
	if reviewDuration == 0 {
		reviewDuration = 60.0
	}

	if err := h.humanModerationServiceV2.SubmitReview(
		c.Request.Context(),
		taskID,
		userID.(uuid.UUID),
		req.Decision,
		req.ViolationTags,
		req.Comment,
		reviewDuration,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
			"code":  "SUBMIT_REVIEW_FAILED",
		})
		return
	}

	var task models.ModerationTask
	h.db.Preload("Video").First(&task, taskID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Review submitted successfully",
		"task_id":  taskID,
		"status":   task.CurrentStatus,
		"decision": req.Decision,
	})
}

func (h *TaskHandler) RollbackTask(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req struct {
		Reason string `json:"reason" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	canRollback, reason, err := h.txManager.CanRollback(c.Request.Context(), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !canRollback {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":        "Cannot rollback this task",
			"reason":       reason,
			"can_rollback": false,
		})
		return
	}

	if err := h.humanModerationServiceV2.RollbackToLatestSnapshot(
		c.Request.Context(),
		taskID,
		userID.(uuid.UUID),
		req.Reason,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
			"code":  "ROLLBACK_FAILED",
		})
		return
	}

	var task models.ModerationTask
	h.db.First(&task, taskID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Task rolled back successfully",
		"task_id":  taskID,
		"status":   task.CurrentStatus,
		"reason":   req.Reason,
	})
}

func (h *TaskHandler) GetTaskSnapshots(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	limit := 20
	if c.Query("limit") != "" {
		fmt.Sscanf(c.Query("limit"), "%d", &limit)
	}

	snapshots, err := h.txManager.GetSnapshotHistory(c.Request.Context(), taskID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"task_id":   taskID,
		"snapshots": snapshots,
		"count":     len(snapshots),
	})
}

func (h *TaskHandler) CheckTaskConsistency(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	isConsistent, inconsistencies, err := h.txManager.VerifyStateConsistency(c.Request.Context(), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"task_id":           taskID,
		"is_consistent":     isConsistent,
		"inconsistencies":   inconsistencies,
		"checked_at":        time.Now(),
	})
}

func (h *TaskHandler) GetInconsistentTasks(c *gin.Context) {
	checkpoints, err := h.recoveryService.GetInconsistentTasks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"inconsistent_tasks": checkpoints,
		"count":               len(checkpoints),
	})
}

func (h *TaskHandler) AssignTask(c *gin.Context) {
	taskIDStr := c.Param("id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req struct {
		ReviewerID string `json:"reviewer_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	reviewerID, err := uuid.Parse(req.ReviewerID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reviewer ID"})
		return
	}

	if err := h.taskDistributor.AssignTask(taskID, reviewerID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task assigned"})
}

func (h *TaskHandler) CreateBatchOperation(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req services.BatchOperationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	valid, warnings := h.batchService.ValidateBatchOperation(req, userID.(uuid.UUID))
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid batch operation", "warnings": warnings})
		return
	}

	operation, err := h.batchService.CreateBatchOperation(userID.(uuid.UUID), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create batch operation"})
		return
	}

	c.JSON(http.StatusCreated, operation)
}

func (h *TaskHandler) GetBatchOperation(c *gin.Context) {
	operationIDStr := c.Param("id")
	operationID, err := uuid.Parse(operationIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid operation ID"})
		return
	}

	operation, err := h.batchService.GetBatchOperationStatus(operationID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Operation not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get operation"})
		return
	}

	c.JSON(http.StatusOK, operation)
}

func (h *TaskHandler) GetMyPendingTasks(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	limit := 20
	if c.Query("limit") != "" {
		fmt.Sscanf(c.Query("limit"), "%d", &limit)
	}

	tasks, err := h.humanModerationService.GetPendingTasks(userID.(uuid.UUID), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get tasks"})
		return
	}

	c.JSON(http.StatusOK, tasks)
}

func (h *TaskHandler) GetMyReviewHistory(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	page := 1
	pageSize := 20

	if c.Query("page") != "" {
		fmt.Sscanf(c.Query("page"), "%d", &page)
	}
	if c.Query("page_size") != "" {
		fmt.Sscanf(c.Query("page_size"), "%d", &pageSize)
	}

	results, total, err := h.humanModerationService.GetReviewHistory(userID.(uuid.UUID), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get review history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"results":     results,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
	})
}
