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

type AppealHandler struct {
	db           *gorm.DB
	redis        *redis.Client
	config       *config.Config
	stateMachine *models.StateMachine
	appealService *services.AppealService
}

func NewAppealHandler(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *AppealHandler {
	return &AppealHandler{
		db:           db,
		redis:        redis,
		config:       cfg,
		stateMachine: sm,
		appealService: services.NewAppealService(db, redis, cfg, sm),
	}
}

type SubmitAppealRequest struct {
	TaskID string `json:"task_id" binding:"required"`
	Reason string `json:"reason" binding:"required"`
}

type ResolveAppealRequest struct {
	Result  string `json:"result" binding:"required"`
	Comment string `json:"comment"`
}

func (h *AppealHandler) SubmitAppeal(c *gin.Context) {
	var req SubmitAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	taskID, err := uuid.Parse(req.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	appeal, err := h.appealService.SubmitAppeal(taskID, userID.(uuid.UUID), req.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, appeal)
}

func (h *AppealHandler) GetPendingAppeals(c *gin.Context) {
	appeals, err := h.appealService.GetPendingAppeals()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get pending appeals"})
		return
	}

	c.JSON(http.StatusOK, appeals)
}

func (h *AppealHandler) GetMyAppeals(c *gin.Context) {
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

	var appeals []models.Appeal
	var total int64

	query := h.db.Model(&models.Appeal{}).Where("submitted_by = ?", userID.(uuid.UUID))
	query.Count(&total)

	offset := (page - 1) * pageSize
	if err := h.db.Preload("Task.Video").Preload("Submitter").
		Where("submitted_by = ?", userID.(uuid.UUID)).
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&appeals).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get appeals"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"appeals":     appeals,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
	})
}

func (h *AppealHandler) AssignAppeal(c *gin.Context) {
	appealIDStr := c.Param("id")
	appealID, err := uuid.Parse(appealIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid appeal ID"})
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

	if err := h.appealService.AssignAppeal(appealID, reviewerID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Appeal assigned"})
}

func (h *AppealHandler) ResolveAppeal(c *gin.Context) {
	appealIDStr := c.Param("id")
	appealID, err := uuid.Parse(appealIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid appeal ID"})
		return
	}

	var req ResolveAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Result != "approve" && req.Result != "reject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid result. Must be 'approve' or 'reject'"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	if err := h.appealService.ResolveAppeal(appealID, userID.(uuid.UUID), req.Result, req.Comment); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Appeal resolved"})
}

func (h *AppealHandler) GetAppealDetails(c *gin.Context) {
	appealIDStr := c.Param("id")
	appealID, err := uuid.Parse(appealIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid appeal ID"})
		return
	}

	appeal, task, frames, autoResult, err := h.appealService.GetAppealDetails(appealID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Appeal not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get appeal details"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"appeal":      appeal,
		"task":        task,
		"frames":      frames,
		"auto_result": autoResult,
	})
}
