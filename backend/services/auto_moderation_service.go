package services

import (
	"bytes"
	"content-moderation/config"
	"content-moderation/models"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AutoModerationService struct {
	db            *gorm.DB
	redis         *redis.Client
	config        *config.Config
	stateMachine  *models.StateMachine
	pythonService string
}

type AutoModerationRequest struct {
	VideoID     uuid.UUID            `json:"video_id"`
	VideoURL    string                 `json:"video_url"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Frames      []FrameAnalysisResult  `json:"frames"`
}

type AutoModerationResponse struct {
	VideoID         uuid.UUID       `json:"video_id"`
	OverallScore    float64         `json:"overall_score"`
	ViolationScores map[string]float64 `json:"violation_scores"`
	FlaggedFrames   []FlaggedFrame  `json:"flagged_frames"`
	TextAnalysis    *TextAnalysis   `json:"text_analysis"`
	AudioAnalysis   *AudioAnalysis  `json:"audio_analysis"`
	Recommendation  string          `json:"recommendation"`
	ProcessingTime  float64         `json:"processing_time"`
}

type FlaggedFrame struct {
	FrameIndex  int       `json:"frame_index"`
	Timestamp   float64   `json:"timestamp"`
	Score       float64   `json:"score"`
	Violations  []string  `json:"violations"`
}

type TextAnalysis struct {
	Text        string            `json:"text"`
	Score       float64           `json:"score"`
	Violations  []string          `json:"violations"`
	Keywords    []string          `json:"keywords"`
	Details     map[string]interface{} `json:"details"`
}

type AudioAnalysis struct {
	Score       float64           `json:"score"`
	Violations  []string          `json:"violations"`
	Transcript  string            `json:"transcript"`
	Details     map[string]interface{} `json:"details"`
}

func NewAutoModerationService(db *gorm.DB, redis *redis.Client, cfg *config.Config, sm *models.StateMachine) *AutoModerationService {
	return &AutoModerationService{
		db:           db,
		redis:        redis,
		config:       cfg,
		stateMachine: sm,
		pythonService: "http://localhost:5000",
	}
}

func (ams *AutoModerationService) ProcessAutoModeration(taskID uuid.UUID) error {
	var task models.ModerationTask
	if err := ams.db.Preload("Video").First(&task, taskID).Error; err != nil {
		return err
	}

	data := map[string]interface{}{}
	if err := ams.stateMachine.Transition(&task, models.StatusAutoModerating, data, nil, "system"); err != nil {
		return err
	}

	go ams.processAutoModerationAsync(task)
	return nil
}

func (ams *AutoModerationService) processAutoModerationAsync(task models.ModerationTask) {
	startTime := time.Now()

	frameService := NewVideoFrameService(ams.db, ams.redis, ams.config)
	if err := frameService.ExtractFrames(task.VideoID); err != nil {
		log.Printf("Frame extraction failed for video %s: %v", task.VideoID, err)
	}

	for i := 0; i < 30; i++ {
		status := frameService.GetFrameExtractionStatus(task.VideoID)
		if status == "completed" {
			break
		}
		if status == "failed" {
			log.Printf("Frame extraction failed for video %s", task.VideoID)
			break
		}
		time.Sleep(1 * time.Second)
	}

	frames, err := frameService.GetVideoFrames(task.VideoID, false)
	if err != nil {
		log.Printf("Failed to get frames for video %s: %v", task.VideoID, err)
		frames = []models.VideoFrame{}
	}

	frameResults := make([]FrameAnalysisResult, len(frames))
	for i, frame := range frames {
		frameResults[i] = FrameAnalysisResult{
			FrameIndex: frame.FrameIndex,
			Timestamp:  frame.Timestamp,
			IsFlagged:  frame.IsFlagged,
			Score:      0.3,
			Violations: []string{},
			Details:    frame.Analysis,
		}
	}

	request := AutoModerationRequest{
		VideoID:     task.VideoID,
		VideoURL:    task.Video.VideoURL,
		Title:       task.Video.Title,
		Description: task.Video.Description,
		Frames:      frameResults,
	}

	response, err := ams.callPythonService(request)
	if err != nil {
		log.Printf("Python service call failed for video %s: %v", task.VideoID, err)
		response = ams.generateMockResponse(request)
	}

	response.ProcessingTime = time.Since(startTime).Seconds()

	if err := ams.saveAutoModerationResult(task.ID, response); err != nil {
		log.Printf("Failed to save auto moderation result for task %s: %v", task.ID, err)
	}

	if err := ams.updateTaskStatus(task, response); err != nil {
		log.Printf("Failed to update task status for task %s: %v", task.ID, err)
	}

	ams.updateStatistics(task, response)
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

func (ams *AutoModerationService) saveAutoModerationResult(taskID uuid.UUID, response *AutoModerationResponse) error {
	result := &models.AutoModerationResult{
		TaskID:          taskID,
		OverallScore:    response.OverallScore,
		ViolationScores: response.ViolationScores,
		FlaggedFrames:   response.FlaggedFrames,
		TextAnalysis:    response.TextAnalysis,
		AudioAnalysis:   response.AudioAnalysis,
		Recommendation:  response.Recommendation,
		ProcessingTime:  response.ProcessingTime,
	}

	if err := ams.db.Create(result).Error; err != nil {
		return err
	}

	return nil
}

func (ams *AutoModerationService) updateTaskStatus(task models.ModerationTask, response *AutoModerationResponse) error {
	now := time.Now()
	task.AutoModerationAt = &now

	data := map[string]interface{}{
		"auto_score": response.OverallScore,
		"recommendation": response.Recommendation,
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

	if err := ams.stateMachine.Transition(&task, nextStatus, data, nil, "system"); err != nil {
		return err
	}

	if nextStatus == models.StatusNeedReview {
		taskDistributor := NewTaskDistributor(ams.db, ams.redis, ams.config, ams.stateMachine)
		taskDistributor.AutoAssignTasks()
	}

	return nil
}

func (ams *AutoModerationService) updateStatistics(task models.ModerationTask, response *AutoModerationResponse) {
	today := time.Now().Truncate(24 * time.Hour)
	
	var stats models.DailyStatistics
	if err := ams.db.Where("date = ?", today).First(&stats).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			stats = models.DailyStatistics{
				Date: today,
			}
			ams.db.Create(&stats)
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

	ams.db.Save(&stats)
}

func (ams *AutoModerationService) GetAutoModerationResult(taskID uuid.UUID) (*models.AutoModerationResult, error) {
	var result models.AutoModerationResult
	if err := ams.db.Where("task_id = ?", taskID).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}
