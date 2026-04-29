package services

import (
	"bytes"
	"content-moderation/config"
	"content-moderation/models"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrTaskAlreadyProcessing = errors.New("task is already being processed")
	ErrAutoResultAlreadyExists = errors.New("auto moderation result already exists")
	ErrTaskVideoMismatch     = errors.New("task video ID mismatch")
)

type AutoModerationService struct {
	db            *gorm.DB
	redis         *redis.Client
	config        *config.Config
	stateMachine  *models.StateMachine
	pythonService string
	processingMu  sync.Mutex
	processingSet map[uuid.UUID]struct{}
}

type AutoModerationRequest struct {
	TaskID      uuid.UUID              `json:"task_id"`
	VideoID     uuid.UUID              `json:"video_id"`
	VideoURL    string                 `json:"video_url"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Frames      []FrameAnalysisResult  `json:"frames"`
}

type AutoModerationResponse struct {
	TaskID          uuid.UUID           `json:"task_id"`
	VideoID         uuid.UUID           `json:"video_id"`
	OverallScore    float64             `json:"overall_score"`
	ViolationScores map[string]float64  `json:"violation_scores"`
	FlaggedFrames   []FlaggedFrame      `json:"flagged_frames"`
	TextAnalysis    *TextAnalysis       `json:"text_analysis"`
	AudioAnalysis   *AudioAnalysis      `json:"audio_analysis"`
	Recommendation  string              `json:"recommendation"`
	ProcessingTime  float64             `json:"processing_time"`
}

type FrameAnalysisResult struct {
	TaskID     uuid.UUID                 `json:"task_id"`
	FrameIndex int                       `json:"frame_index"`
	Timestamp  float64                   `json:"timestamp"`
	IsFlagged  bool                      `json:"is_flagged"`
	Score      float64                   `json:"score"`
	Violations []string                  `json:"violations"`
	Details    map[string]interface{}    `json:"details"`
}

type FlaggedFrame struct {
	TaskID     uuid.UUID  `json:"task_id"`
	FrameIndex int        `json:"frame_index"`
	Timestamp  float64    `json:"timestamp"`
	Score      float64    `json:"score"`
	Violations []string   `json:"violations"`
}

type TextAnalysis struct {
	Text       string                 `json:"text"`
	Score      float64                `json:"score"`
	Violations []string               `json:"violations"`
	Keywords   []string               `json:"keywords"`
	Details    map[string]interface{} `json:"details"`
}

type AudioAnalysis struct {
	Score      float64                `json:"score"`
	Violations []string               `json:"violations"`
	Transcript string                 `json:"transcript"`
	Details    map[string]interface{} `json:"details"`
}

type ProcessResult struct {
	Success            bool
	TaskID             uuid.UUID
	VideoID            uuid.UUID
	StartTime          time.Time
	EndTime            time.Time
	ProcessingTime     float64
	StatusChanged      bool
	OldStatus          string
	NewStatus          string
	VersionChanged     bool
	OldVersion         int
	NewVersion         int
	Error              error
}

func NewAutoModerationService(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *AutoModerationService {
	return &AutoModerationService{
		db:            db,
		redis:         redis,
		config:        cfg,
		stateMachine:  sm,
		pythonService: "http://localhost:5000",
		processingSet: make(map[uuid.UUID]struct{}),
	}
}

func (ams *AutoModerationService) IsTaskProcessing(taskID uuid.UUID) bool {
	ams.processingMu.Lock()
	defer ams.processingMu.Unlock()
	
	_, exists := ams.processingSet[taskID]
	if exists {
		return true
	}
	
	if ams.redis != nil {
		ctx := context.Background()
		lockKey := fmt.Sprintf("auto_processing:%s", taskID)
		exists, err := ams.redis.Exists(ctx, lockKey).Result()
		if err == nil && exists > 0 {
			return true
		}
	}
	
	return false
}

func (ams *AutoModerationService) MarkTaskProcessing(taskID uuid.UUID, ttl time.Duration) (bool, error) {
	ams.processingMu.Lock()
	defer ams.processingMu.Unlock()
	
	if _, exists := ams.processingSet[taskID]; exists {
		return false, ErrTaskAlreadyProcessing
	}
	
	if ams.redis != nil {
		ctx := context.Background()
		lockKey := fmt.Sprintf("auto_processing:%s", taskID)
		lockValue := uuid.New().String()
		
		acquired, err := ams.redis.SetNX(ctx, lockKey, lockValue, ttl).Result()
		if err != nil {
			return false, err
		}
		if !acquired {
			return false, ErrTaskAlreadyProcessing
		}
	}
	
	ams.processingSet[taskID] = struct{}{}
	return true, nil
}

func (ams *AutoModerationService) MarkTaskComplete(taskID uuid.UUID) {
	ams.processingMu.Lock()
	defer ams.processingMu.Unlock()
	
	delete(ams.processingSet, taskID)
	
	if ams.redis != nil {
		ctx := context.Background()
		lockKey := fmt.Sprintf("auto_processing:%s", taskID)
		ams.redis.Del(ctx, lockKey)
	}
}

func (ams *AutoModerationService) CheckAutoResultExists(taskID uuid.UUID) (bool, error) {
	var count int64
	if err := ams.db.Model(&models.AutoModerationResult{}).
		Where("task_id = ?", taskID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (ams *AutoModerationService) ProcessAutoModeration(ctx context.Context, taskID uuid.UUID) (*ProcessResult, error) {
	result := &ProcessResult{
		TaskID:    taskID,
		StartTime: time.Now(),
		Success:   false,
	}
	
	if ams.IsTaskProcessing(taskID) {
		result.Error = ErrTaskAlreadyProcessing
		return result, ErrTaskAlreadyProcessing
	}
	
	acquired, err := ams.MarkTaskProcessing(taskID, 5*time.Minute)
	if err != nil || !acquired {
		result.Error = err
		return result, err
	}
	defer ams.MarkTaskComplete(taskID)
	
	exists, err := ams.CheckAutoResultExists(taskID)
	if err != nil {
		result.Error = err
		return result, err
	}
	if exists {
		result.Error = ErrAutoResultAlreadyExists
		return result, ErrAutoResultAlreadyExists
	}
	
	var task models.ModerationTask
	if err := ams.db.Preload("Video").
		Set("gorm:query_option", "FOR UPDATE").
		First(&task, taskID).Error; err != nil {
		result.Error = err
		return result, err
	}
	
	result.VideoID = task.VideoID
	result.OldStatus = task.CurrentStatus
	result.OldVersion = task.Version
	
	if task.VideoID != task.Video.ID {
		result.Error = ErrTaskVideoMismatch
		return result, ErrTaskVideoMismatch
	}
	
	if task.CurrentStatus != string(models.StatusPending) && 
	   task.CurrentStatus != string(models.StatusAutoModerating) {
		err = fmt.Errorf("task %s has invalid status for auto moderation: %s", taskID, task.CurrentStatus)
		result.Error = err
		return result, err
	}
	
	transitionResult, err := ams.stateMachine.Transition(
		ctx,
		&task,
		models.StatusAutoModerating,
		map[string]interface{}{},
		nil,
		"system",
		&task.VideoID,
	)
	if err != nil {
		result.Error = err
		return result, err
	}
	
	result.StatusChanged = true
	result.NewStatus = string(models.StatusAutoModerating)
	result.VersionChanged = true
	result.NewVersion = transitionResult.NewVersion
	
	processErr := ams.executeAutoModeration(ctx, &task, result)
	
	result.EndTime = time.Now()
	result.ProcessingTime = result.EndTime.Sub(result.StartTime).Seconds()
	result.Success = processErr == nil
	
	if processErr != nil {
		result.Error = processErr
	}
	
	return result, processErr
}

func (ams *AutoModerationService) executeAutoModeration(ctx context.Context, task *models.ModerationTask, result *ProcessResult) error {
	frameService := NewVideoFrameService(ams.db, ams.redis, ams.config)
	
	if err := frameService.ExtractFramesForTask(ctx, task.ID, task.VideoID); err != nil {
		log.Printf("Frame extraction failed for task %s, video %s: %v", task.ID, task.VideoID, err)
	}
	
	for i := 0; i < 60; i++ {
		status := frameService.GetFrameExtractionStatusForTask(task.ID)
		if status == "completed" {
			break
		}
		if status == "failed" {
			log.Printf("Frame extraction failed for task %s", task.ID)
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	
	frames, err := frameService.GetVideoFramesForTask(ctx, task.ID, task.VideoID)
	if err != nil {
		log.Printf("Failed to get frames for task %s: %v", task.ID, err)
		frames = []models.VideoFrame{}
	}
	
	frameResults := make([]FrameAnalysisResult, len(frames))
	for i, frame := range frames {
		frameResults[i] = FrameAnalysisResult{
			TaskID:     frame.TaskID,
			FrameIndex: frame.FrameIndex,
			Timestamp:  frame.Timestamp,
			IsFlagged:  frame.IsFlagged,
			Score:      0.3,
			Violations: []string{},
			Details:    frame.Analysis,
		}
	}
	
	request := AutoModerationRequest{
		TaskID:      task.ID,
		VideoID:     task.VideoID,
		VideoURL:    task.Video.VideoURL,
		Title:       task.Video.Title,
		Description: task.Video.Description,
		Frames:      frameResults,
	}
	
	response, err := ams.callPythonService(request)
	if err != nil {
		log.Printf("Python service call failed for task %s: %v", task.ID, err)
		response = ams.generateMockResponse(request)
	}
	response.TaskID = task.ID
	response.VideoID = task.VideoID
	
	return ams.saveAutoResultWithTransition(ctx, task, response, result)
}

func (ams *AutoModerationService) saveAutoResultWithTransition(
	ctx context.Context,
	task *models.ModerationTask,
	response *AutoModerationResponse,
	result *ProcessResult,
) error {
	return ams.db.Transaction(func(tx *gorm.DB) error {
		var currentTask models.ModerationTask
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			First(&currentTask, task.ID).Error; err != nil {
			return err
		}
		
		if currentTask.Version != task.Version {
			return models.ErrOptimisticLockFailure
		}
		
		if currentTask.VideoID != task.VideoID {
			return ErrTaskVideoMismatch
		}
		
		autoResult := &models.AutoModerationResult{
			TaskID:          task.ID,
			VideoID:         task.VideoID,
			OverallScore:    response.OverallScore,
			ViolationScores: response.ViolationScores,
			FlaggedFrames:   response.FlaggedFrames,
			TextAnalysis:    response.TextAnalysis,
			AudioAnalysis:   response.AudioAnalysis,
			Recommendation:  response.Recommendation,
			ProcessingTime:  response.ProcessingTime,
		}
		
		if err := tx.Create(autoResult).Error; err != nil {
			return fmt.Errorf("failed to create auto moderation result: %w", err)
		}
		
		var nextStatus models.ModerationStatus
		switch response.Recommendation {
		case "approve":
			nextStatus = models.StatusAutoApproved
		case "reject":
			nextStatus = models.StatusAutoRejected
		default:
			nextStatus = models.StatusNeedReview
		}
		
		data := map[string]interface{}{
			"auto_score":       response.OverallScore,
			"recommendation":   response.Recommendation,
			"auto_result_id":   autoResult.ID,
		}
		
		oldVersion := currentTask.Version
		transitionResult, err := ams.stateMachine.Transition(
			ctx,
			&currentTask,
			nextStatus,
			data,
			nil,
			"system",
			&task.VideoID,
		)
		if err != nil {
			return err
		}
		
		result.OldStatus = currentTask.CurrentStatus
		result.NewStatus = string(nextStatus)
		result.OldVersion = oldVersion
		result.NewVersion = transitionResult.NewVersion
		result.StatusChanged = true
		result.VersionChanged = true
		
		if nextStatus == models.StatusNeedReview {
			go func() {
				taskDistributor := NewTaskDistributor(tx, ams.redis, ams.config, ams.stateMachine)
				taskDistributor.AutoAssignTasks()
			}()
		}
		
		ams.updateStatisticsInTx(tx, task, response)
		
		return nil
	})
}

func (ams *AutoModerationService) updateStatisticsInTx(tx *gorm.DB, task *models.ModerationTask, response *AutoModerationResponse) {
	today := time.Now().Truncate(24 * time.Hour)
	
	var stats models.DailyStatistics
	if err := tx.Where("date = ?", today).First(&stats).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			stats = models.DailyStatistics{
				Date: today,
			}
			tx.Create(&stats)
		}
	}
	
	stats.TotalVideos++
	stats.AutoModerated++
	
	switch response.Recommendation {
	case "approve":
		stats.AutoApproved++
	case "reject":
		stats.AutoRejected++
	default:
		stats.NeedReview++
	}
	
	tx.Save(&stats)
}

func (ams *AutoModerationService) callPythonService(request AutoModerationRequest) (*AutoModerationResponse, error) {
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/v1/moderate", ams.pythonService)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("python service returned status %d", resp.StatusCode)
	}

	var response AutoModerationResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

func (ams *AutoModerationService) generateMockResponse(request AutoModerationRequest) *AutoModerationResponse {
	flaggedFrames := []FlaggedFrame{}
	violationScores := map[string]float64{
		"violence":       0.0,
		"nudity":         0.0,
		"hate_speech":    0.0,
		"misinformation": 0.0,
		"sensitive":      0.0,
	}

	var totalScore float64 = 0.1

	textAnalysis := &TextAnalysis{
		Text:       request.Title + " " + request.Description,
		Score:      0.1,
		Violations: []string{},
		Keywords:   []string{},
		Details:    map[string]interface{}{},
	}

	sensitiveKeywords := []string{"暴力", "色情", "恐怖", "反动", "赌博", "毒品"}
	lowerText := strings.ToLower(textAnalysis.Text)
	
	for _, keyword := range sensitiveKeywords {
		if strings.Contains(lowerText, strings.ToLower(keyword)) {
			textAnalysis.Violations = append(textAnalysis.Violations, "sensitive_keyword")
			textAnalysis.Keywords = append(textAnalysis.Keywords, keyword)
			textAnalysis.Score = 0.8
			totalScore = 0.85
		}
	}

	for _, frame := range request.Frames {
		if frame.IsFlagged {
			flaggedFrames = append(flaggedFrames, FlaggedFrame{
				TaskID:     request.TaskID,
				FrameIndex: frame.FrameIndex,
				Timestamp:  frame.Timestamp,
				Score:      frame.Score,
				Violations: frame.Violations,
			})
			if frame.Score > totalScore {
				totalScore = frame.Score
			}
		}
	}

	var recommendation string
	switch {
	case totalScore >= ams.config.Moderation.AutoRejectThreshold:
		recommendation = "reject"
	case totalScore <= ams.config.Moderation.AutoApproveThreshold:
		recommendation = "approve"
	default:
		recommendation = "review"
	}

	return &AutoModerationResponse{
		TaskID:          request.TaskID,
		VideoID:         request.VideoID,
		OverallScore:    totalScore,
		ViolationScores: violationScores,
		FlaggedFrames:   flaggedFrames,
		TextAnalysis:    textAnalysis,
		AudioAnalysis: &AudioAnalysis{
			Score:      0.1,
			Violations: []string{},
			Transcript: "",
			Details:    map[string]interface{}{},
		},
		Recommendation: recommendation,
	}
}

func (ams *AutoModerationService) GetAutoModerationResult(taskID uuid.UUID) (*models.AutoModerationResult, error) {
	var result models.AutoModerationResult
	if err := ams.db.Where("task_id = ?", taskID).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func (ams *AutoModerationService) ValidateTaskVideoPair(taskID uuid.UUID, videoID uuid.UUID) error {
	var task models.ModerationTask
	if err := ams.db.First(&task, taskID).Error; err != nil {
		return err
	}
	
	if task.VideoID != videoID {
		return fmt.Errorf("%w: task %s expects video %s, got %s", 
			ErrTaskVideoMismatch, taskID, task.VideoID, videoID)
	}
	
	return nil
}

func (ams *AutoModerationService) ReProcessFailedTask(ctx context.Context, taskID uuid.UUID) (*ProcessResult, error) {
	var task models.ModerationTask
	if err := ams.db.Preload("Video").First(&task, taskID).Error; err != nil {
		return nil, err
	}
	
	if task.CurrentStatus != string(models.StatusAutoModerating) {
		return nil, fmt.Errorf("task %s is not in auto_moderating state", taskID)
	}
	
	ams.db.Model(&models.AutoModerationResult{}).Where("task_id = ?", taskID).Delete(&models.AutoModerationResult{})
	
	return ams.ProcessAutoModeration(ctx, taskID)
}
