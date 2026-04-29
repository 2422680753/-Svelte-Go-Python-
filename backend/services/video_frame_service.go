package services

import (
	"bytes"
	"context"
	"content-moderation/config"
	"content-moderation/models"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type VideoFrameService struct {
	db          *gorm.DB
	redis       *redis.Client
	config      *config.Config
	frameDir    string
	workerPool  chan struct{}
	frameCache  *FrameCacheService
	mappingSvc  *models.FrameMappingService
}

type FrameExtractionJob struct {
	TaskID     uuid.UUID
	VideoID    uuid.UUID
	VideoURL   string
	Duration   int
}

type ExtractedFrame struct {
	TaskID         uuid.UUID
	VideoID        uuid.UUID
	FrameIndex     int
	ActualIndex    int
	Timestamp      float64
	ActualTime     float64
	FramePath      string
	FrameURL       string
	Analysis       map[string]interface{}
	IsFlagged      bool
	Score          float64
	Violations     []string
}

type FrameSegmentInfo struct {
	StartIndex   int
	EndIndex     int
	StartTime    float64
	EndTime      float64
	FrameCount   int
	IsLoaded     bool
}

type FrameMapping struct {
	TaskID        uuid.UUID
	VideoID       uuid.UUID
	FrameCount    int
	FrameInterval float64
	Duration      float64
	IndexToTime   map[int]float64
	TimeToIndex   map[float64]int
	Segments      []FrameSegmentInfo
}

type ExtractionProgress struct {
	TaskID      uuid.UUID
	Status      string
	TotalFrames int
	Extracted   int
	Saved       int
	StartTime   time.Time
	EndTime     time.Time
	Error       string
}

type FrameCacheService struct {
	db          *gorm.DB
	redis       *redis.Client
	cacheSize   int
	maxTTL      time.Duration
}

func NewFrameCacheService(db *gorm.DB, redis *redis.Client, cfg *config.Config) *FrameCacheService {
	return &FrameCacheService{
		db:        db,
		redis:     redis,
		cacheSize: 1000,
		maxTTL:    1 * time.Hour,
	}
}

func (fcs *FrameCacheService) GetFrameByIndex(ctx context.Context, taskID uuid.UUID, frameIndex int) (*models.VideoFrame, error) {
	cacheKey := fmt.Sprintf("frame_cache:task:%s:index:%d", taskID, frameIndex)
	
	if fcs.redis != nil {
		var frame models.VideoFrame
		data, err := fcs.redis.Get(ctx, cacheKey).Bytes()
		if err == nil {
			if json.Unmarshal(data, &frame) == nil {
				return &frame, nil
			}
		}
	}
	
	var frame models.VideoFrame
	if err := fcs.db.Where("task_id = ? AND frame_index = ?", taskID, frameIndex).
		First(&frame).Error; err != nil {
		return nil, err
	}
	
	if fcs.redis != nil {
		data, _ := json.Marshal(frame)
		fcs.redis.SetEX(ctx, cacheKey, data, fcs.maxTTL)
	}
	
	return &frame, nil
}

func (fcs *FrameCacheService) GetFramesByTimeRange(ctx context.Context, taskID uuid.UUID, startTime, endTime float64) ([]models.VideoFrame, error) {
	var frames []models.VideoFrame
	if err := fcs.db.Where("task_id = ? AND timestamp >= ? AND timestamp <= ?", taskID, startTime, endTime).
		Order("timestamp ASC").
		Find(&frames).Error; err != nil {
		return nil, err
	}
	return frames, nil
}

func (fcs *FrameCacheService) GetFramesPaginated(ctx context.Context, taskID uuid.UUID, page, pageSize int) ([]models.VideoFrame, int64, error) {
	var total int64
	fcs.db.Model(&models.VideoFrame{}).Where("task_id = ?", taskID).Count(&total)
	
	var frames []models.VideoFrame
	offset := (page - 1) * pageSize
	if err := fcs.db.Where("task_id = ?", taskID).
		Order("frame_index ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&frames).Error; err != nil {
		return nil, 0, err
	}
	
	return frames, total, nil
}

func (fcs *FrameCacheService) CacheFrames(ctx context.Context, taskID uuid.UUID, frames []models.VideoFrame) error {
	if fcs.redis == nil {
		return nil
	}
	
	pipe := fcs.redis.Pipeline()
	for _, frame := range frames {
		cacheKey := fmt.Sprintf("frame_cache:task:%s:index:%d", taskID, frame.FrameIndex)
		data, _ := json.Marshal(frame)
		pipe.SetEX(ctx, cacheKey, data, fcs.maxTTL)
	}
	
	_, err := pipe.Exec(ctx)
	return err
}

func (fcs *FrameCacheService) InvalidateTaskCache(ctx context.Context, taskID uuid.UUID) error {
	if fcs.redis == nil {
		return nil
	}
	
	pattern := fmt.Sprintf("frame_cache:task:%s:*", taskID)
	iter := fcs.redis.Scan(ctx, 0, pattern, 0).Iterator()
	
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	
	if err := iter.Err(); err != nil {
		return err
	}
	
	if len(keys) > 0 {
		return fcs.redis.Del(ctx, keys...).Err()
	}
	
	return nil
}

func NewVideoFrameService(db *gorm.DB, redis *redis.Client, cfg *config.Config) *VideoFrameService {
	frameDir := filepath.Join(os.TempDir(), "video_frames")
	os.MkdirAll(frameDir, 0755)

	return &VideoFrameService{
		db:         db,
		redis:      redis,
		config:     cfg,
		frameDir:   frameDir,
		workerPool: make(chan struct{}, 10),
		frameCache: NewFrameCacheService(db, redis, cfg),
	}
}

func (vfs *VideoFrameService) GetProgressKey(taskID uuid.UUID) string {
	return fmt.Sprintf("frame_extraction:progress:%s", taskID)
}

func (vfs *VideoFrameService) updateProgress(taskID uuid.UUID, progress ExtractionProgress) {
	if vfs.redis == nil {
		return
	}
	
	ctx := context.Background()
	key := vfs.GetProgressKey(taskID)
	data, _ := json.Marshal(progress)
	vfs.redis.SetEX(ctx, key, data, 1*time.Hour)
}

func (vfs *VideoFrameService) GetFrameExtractionStatusForTask(taskID uuid.UUID) string {
	if vfs.redis == nil {
		return "not_started"
	}
	
	ctx := context.Background()
	key := vfs.GetProgressKey(taskID)
	
	var progress ExtractionProgress
	data, err := vfs.redis.Get(ctx, key).Bytes()
	if err != nil {
		return "not_started"
	}
	
	json.Unmarshal(data, &progress)
	return progress.Status
}

func (vfs *VideoFrameService) ExtractFramesForTask(ctx context.Context, taskID uuid.UUID, videoID uuid.UUID) error {
	var task models.ModerationTask
	if err := vfs.db.Preload("Video").First(&task, taskID).Error; err != nil {
		return err
	}
	
	if task.VideoID != videoID {
		return fmt.Errorf("task video ID mismatch: task=%s, expected=%s, got=%s", 
			taskID, task.VideoID, videoID)
	}
	
	var existingCount int64
	vfs.db.Model(&models.VideoFrame{}).Where("task_id = ?", taskID).Count(&existingCount)
	if existingCount > 0 {
		return fmt.Errorf("frames already exist for task %s", taskID)
	}
	
	progress := ExtractionProgress{
		TaskID:    taskID,
		Status:    "processing",
		StartTime: time.Now(),
	}
	vfs.updateProgress(taskID, progress)
	
	job := FrameExtractionJob{
		TaskID:   taskID,
		VideoID:  videoID,
		VideoURL: task.Video.VideoURL,
		Duration: task.Video.Duration,
	}
	
	vfs.workerPool <- struct{}{}
	go func() {
		defer func() { <-vfs.workerPool }()
		
		extractedFrames, err := vfs.processFrameExtraction(ctx, job)
		if err != nil {
			log.Printf("Frame extraction failed for task %s: %v", taskID, err)
			progress.Status = "failed"
			progress.Error = err.Error()
			progress.EndTime = time.Now()
			vfs.updateProgress(taskID, progress)
			return
		}
		
		if err := vfs.saveFramesWithMapping(ctx, taskID, videoID, extractedFrames); err != nil {
			log.Printf("Failed to save frames for task %s: %v", taskID, err)
			progress.Status = "failed"
			progress.Error = err.Error()
			progress.EndTime = time.Now()
			vfs.updateProgress(taskID, progress)
			return
		}
		
		progress.Status = "completed"
		progress.Extracted = len(extractedFrames)
		progress.Saved = len(extractedFrames)
		progress.EndTime = time.Now()
		vfs.updateProgress(taskID, progress)
	}()
	
	return nil
}

func (vfs *VideoFrameService) processFrameExtraction(ctx context.Context, job FrameExtractionJob) ([]ExtractedFrame, error) {
	localPath, err := vfs.downloadVideo(job.VideoURL, job.TaskID)
	if err != nil {
		return nil, err
	}
	defer os.Remove(localPath)
	
	actualDuration, err := vfs.getActualDuration(localPath)
	if err != nil {
		log.Printf("Warning: could not get actual duration, using estimate: %v", err)
		actualDuration = float64(job.Duration)
	}
	
	frameInterval := float64(vfs.config.Moderation.VideoFrameInterval)
	estimatedFrames := int(math.Ceil(actualDuration / frameInterval))
	if estimatedFrames < 1 {
		estimatedFrames = 1
	}
	
	progress := ExtractionProgress{
		TaskID:      job.TaskID,
		Status:      "processing",
		TotalFrames: estimatedFrames,
		StartTime:   time.Now(),
	}
	vfs.updateProgress(job.TaskID, progress)
	
	frameResults, err := vfs.extractAllFrames(ctx, job, localPath, actualDuration, frameInterval)
	if err != nil {
		return nil, err
	}
	
	sort.Slice(frameResults, func(i, j int) bool {
		return frameResults[i].Timestamp < frameResults[j].Timestamp
	})
	
	for i := range frameResults {
		frameResults[i].ActualIndex = i
	}
	
	return frameResults, nil
}

func (vfs *VideoFrameService) getActualDuration(videoPath string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)
	
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	
	var duration float64
	fmt.Sscanf(strings.TrimSpace(stdout.String()), "%f", &duration)
	return duration, nil
}

func (vfs *VideoFrameService) extractAllFrames(
	ctx context.Context,
	job FrameExtractionJob,
	localPath string,
	duration float64,
	frameInterval float64,
) ([]ExtractedFrame, error) {
	
	var wg sync.WaitGroup
	var mu sync.Mutex
	
	estimatedFrames := int(math.Ceil(duration / frameInterval))
	if estimatedFrames < 1 {
		estimatedFrames = 1
	}
	
	frames := make([]ExtractedFrame, 0, estimatedFrames)
	errorsChan := make(chan error, estimatedFrames)
	
	maxWorkers := 5
	semaphore := make(chan struct{}, maxWorkers)
	
	for i := 0; i < estimatedFrames; i++ {
		timestamp := float64(i) * frameInterval
		if timestamp >= duration && i > 0 {
			continue
		}
		
		frameIndex := i
		
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case semaphore <- struct{}{}:
			wg.Add(1)
			go func(idx int, ts float64) {
				defer wg.Done()
				defer func() { <-semaphore }()
				
				frame, err := vfs.extractAndAnalyzeFrame(ctx, job, localPath, idx, ts)
				if err != nil {
					errorsChan <- err
					return
				}
				
				mu.Lock()
				frames = append(frames, *frame)
				mu.Unlock()
				
			}(frameIndex, timestamp)
		}
	}
	
	wg.Wait()
	close(errorsChan)
	
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames extracted successfully")
	}
	
	return frames, nil
}

func (vfs *VideoFrameService) extractAndAnalyzeFrame(
	ctx context.Context,
	job FrameExtractionJob,
	videoPath string,
	frameIndex int,
	timestamp float64,
) (*ExtractedFrame, error) {
	
	frameBaseName := fmt.Sprintf("%s_frame_%06d", job.TaskID, frameIndex)
	framePath := filepath.Join(vfs.frameDir, frameBaseName+".jpg")
	
	actualTime, err := vfs.extractFrameAtTime(videoPath, framePath, timestamp)
	if err != nil {
		return nil, err
	}
	defer os.Remove(framePath)
	
	analysisResult, err := vfs.analyzeFrame(framePath)
	if err != nil {
		return nil, err
	}
	
	frameURL := vfs.generateFrameURL(job.TaskID, frameIndex, actualTime)
	
	return &ExtractedFrame{
		TaskID:     job.TaskID,
		VideoID:    job.VideoID,
		FrameIndex: frameIndex,
		Timestamp:  timestamp,
		ActualTime: actualTime,
		FramePath:  framePath,
		FrameURL:   frameURL,
		Analysis:   analysisResult.Details,
		IsFlagged:  analysisResult.IsFlagged,
		Score:      analysisResult.Score,
		Violations: analysisResult.Violations,
	}, nil
}

func (vfs *VideoFrameService) extractFrameAtTime(videoPath string, outputPath string, targetTime float64) (float64, error) {
	cmd := exec.Command("ffmpeg",
		"-y",
		"-ss", fmt.Sprintf("%.6f", targetTime),
		"-i", videoPath,
		"-vframes", "1",
		"-q:v", "2",
		"-f", "image2",
		outputPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return targetTime, fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	if _, err := os.Stat(outputPath); err != nil {
		return targetTime, fmt.Errorf("frame file not created: %w", err)
	}

	return targetTime, nil
}

func (vfs *VideoFrameService) generateFrameURL(taskID uuid.UUID, frameIndex int, timestamp float64) string {
	return fmt.Sprintf("http://localhost:9000/frames/%s/%06d_%.3f.jpg", taskID, frameIndex, timestamp)
}

func (vfs *VideoFrameService) saveFramesWithMapping(
	ctx context.Context,
	taskID uuid.UUID,
	videoID uuid.UUID,
	extractedFrames []ExtractedFrame,
) error {
	
	return vfs.db.Transaction(func(tx *gorm.DB) error {
		if len(extractedFrames) == 0 {
			return fmt.Errorf("no frames to save")
		}
		
		dbFrames := make([]models.VideoFrame, 0, len(extractedFrames))
		
		for _, ef := range extractedFrames {
			dbFrames = append(dbFrames, models.VideoFrame{
				TaskID:     ef.TaskID,
				VideoID:    ef.VideoID,
				FrameIndex: ef.ActualIndex,
				Timestamp:  ef.ActualTime,
				FrameURL:   ef.FrameURL,
				Analysis:   ef.Analysis,
				IsFlagged:  ef.IsFlagged,
			})
		}
		
		batchSize := 100
		for i := 0; i < len(dbFrames); i += batchSize {
			end := i + batchSize
			if end > len(dbFrames) {
				end = len(dbFrames)
			}
			
			batch := dbFrames[i:end]
			if err := tx.Create(&batch).Error; err != nil {
				return fmt.Errorf("failed to save frames batch %d-%d: %w", i, end-1, err)
			}
		}
		
		indexToTime := make(map[int]float64)
		timeToIndex := make(map[float64]int)
		var maxTime float64
		
		for _, f := range dbFrames {
			indexToTime[f.FrameIndex] = f.Timestamp
			timeToIndex[f.Timestamp] = f.FrameIndex
			if f.Timestamp > maxTime {
				maxTime = f.Timestamp
			}
		}
		
		var frameInterval float64
		if len(dbFrames) > 1 {
			frameInterval = dbFrames[1].Timestamp - dbFrames[0].Timestamp
		}
		
		segmentSize := 50
		segments := []FrameSegmentInfo{}
		
		for i := 0; i < len(dbFrames); i += segmentSize {
			end := i + segmentSize
			if end > len(dbFrames) {
				end = len(dbFrames)
			}
			
			segmentFrames := dbFrames[i:end]
			segments = append(segments, FrameSegmentInfo{
				StartIndex: segmentFrames[0].FrameIndex,
				EndIndex:   segmentFrames[len(segmentFrames)-1].FrameIndex,
				StartTime:  segmentFrames[0].Timestamp,
				EndTime:    segmentFrames[len(segmentFrames)-1].Timestamp,
				FrameCount: len(segmentFrames),
				IsLoaded:   true,
			})
		}
		
		mapping := &FrameMapping{
			TaskID:        taskID,
			VideoID:       videoID,
			FrameCount:    len(dbFrames),
			FrameInterval: frameInterval,
			Duration:      maxTime,
			IndexToTime:   indexToTime,
			TimeToIndex:   timeToIndex,
			Segments:      segments,
		}
		
		if vfs.redis != nil {
			mappingKey := fmt.Sprintf("frame_mapping:%s", taskID)
			mappingData, _ := json.Marshal(mapping)
			vfs.redis.SetEX(ctx, mappingKey, mappingData, 24*time.Hour)
			
			timeIndexKey := fmt.Sprintf("frame_time_index:%s", taskID)
			pipe := vfs.redis.Pipeline()
			for ts, idx := range timeToIndex {
				pipe.ZAdd(ctx, timeIndexKey, &redis.Z{
					Score:  ts,
					Member: idx,
				})
			}
			pipe.Expire(ctx, timeIndexKey, 24*time.Hour)
			pipe.Exec(ctx)
		}
		
		return nil
	})
}

func (vfs *VideoFrameService) GetVideoFramesForTask(ctx context.Context, taskID uuid.UUID, videoID uuid.UUID) ([]models.VideoFrame, error) {
	if videoID != uuid.Nil {
		var task models.ModerationTask
		if err := vfs.db.First(&task, taskID).Error; err != nil {
			return nil, err
		}
		if task.VideoID != videoID {
			return nil, fmt.Errorf("task video ID mismatch")
		}
	}
	
	var frames []models.VideoFrame
	if err := vfs.db.Where("task_id = ?", taskID).
		Order("frame_index ASC").
		Find(&frames).Error; err != nil {
		return nil, err
	}
	
	return frames, nil
}

func (vfs *VideoFrameService) GetFrameForTask(ctx context.Context, taskID uuid.UUID, frameIndex int) (*models.VideoFrame, error) {
	return vfs.frameCache.GetFrameByIndex(ctx, taskID, frameIndex)
}

func (vfs *VideoFrameService) FindFrameByTime(ctx context.Context, taskID uuid.UUID, timestamp float64) (*models.VideoFrame, error) {
	if vfs.redis != nil {
		timeIndexKey := fmt.Sprintf("frame_time_index:%s", taskID)
		
		results, err := vfs.redis.ZRangeByScoreWithScores(ctx, timeIndexKey, &redis.ZRangeBy{
			Min:    "-inf",
			Max:    fmt.Sprintf("%f", timestamp),
			Offset: 0,
			Count:  1,
		}).Result()
		
		if err == nil && len(results) > 0 {
			var frameIndex int
			if idx, ok := results[0].Member.(int); ok {
				frameIndex = idx
			} else {
				fmt.Sscanf(fmt.Sprintf("%v", results[0].Member), "%d", &frameIndex)
			}
			return vfs.frameCache.GetFrameByIndex(ctx, taskID, frameIndex)
		}
	}
	
	var frames []models.VideoFrame
	if err := vfs.db.Where("task_id = ? AND timestamp <= ?", taskID, timestamp).
		Order("timestamp DESC").
		Limit(1).
		Find(&frames).Error; err != nil {
		return nil, err
	}
	
	if len(frames) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	
	return &frames[0], nil
}

func (vfs *VideoFrameService) GetFramesPaginated(ctx context.Context, taskID uuid.UUID, page, pageSize int) ([]models.VideoFrame, int64, error) {
	return vfs.frameCache.GetFramesPaginated(ctx, taskID, page, pageSize)
}

func (vfs *VideoFrameService) GetFlaggedFrames(ctx context.Context, taskID uuid.UUID) ([]models.VideoFrame, error) {
	var frames []models.VideoFrame
	if err := vfs.db.Where("task_id = ? AND is_flagged = ?", taskID, true).
		Order("frame_index ASC").
		Find(&frames).Error; err != nil {
		return nil, err
	}
	return frames, nil
}

func (vfs *VideoFrameService) GetFrameMapping(ctx context.Context, taskID uuid.UUID) (*FrameMapping, error) {
	if vfs.redis != nil {
		mappingKey := fmt.Sprintf("frame_mapping:%s", taskID)
		var mapping FrameMapping
		
		data, err := vfs.redis.Get(ctx, mappingKey).Bytes()
		if err == nil {
			if json.Unmarshal(data, &mapping) == nil {
				return &mapping, nil
			}
		}
	}
	
	var frames []models.VideoFrame
	if err := vfs.db.Where("task_id = ?", taskID).
		Order("frame_index ASC").
		Find(&frames).Error; err != nil {
		return nil, err
	}
	
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames found for task %s", taskID)
	}
	
	indexToTime := make(map[int]float64)
	timeToIndex := make(map[float64]int)
	var maxTime float64
	
	for _, f := range frames {
		indexToTime[f.FrameIndex] = f.Timestamp
		timeToIndex[f.Timestamp] = f.FrameIndex
		if f.Timestamp > maxTime {
			maxTime = f.Timestamp
		}
	}
	
	var frameInterval float64
	if len(frames) > 1 {
		frameInterval = frames[1].Timestamp - frames[0].Timestamp
	}
	
	segmentSize := 50
	segments := []FrameSegmentInfo{}
	
	for i := 0; i < len(frames); i += segmentSize {
		end := i + segmentSize
		if end > len(frames) {
			end = len(frames)
		}
		
		segmentFrames := frames[i:end]
		segments = append(segments, FrameSegmentInfo{
			StartIndex: segmentFrames[0].FrameIndex,
			EndIndex:   segmentFrames[len(segmentFrames)-1].FrameIndex,
			StartTime:  segmentFrames[0].Timestamp,
			EndTime:    segmentFrames[len(segmentFrames)-1].Timestamp,
			FrameCount: len(segmentFrames),
			IsLoaded:   true,
		})
	}
	
	mapping := &FrameMapping{
		TaskID:        taskID,
		VideoID:       frames[0].VideoID,
		FrameCount:    len(frames),
		FrameInterval: frameInterval,
		Duration:      maxTime,
		IndexToTime:   indexToTime,
		TimeToIndex:   timeToIndex,
		Segments:      segments,
	}
	
	if vfs.redis != nil {
		mappingKey := fmt.Sprintf("frame_mapping:%s", taskID)
		mappingData, _ := json.Marshal(mapping)
		vfs.redis.SetEX(ctx, mappingKey, mappingData, 24*time.Hour)
	}
	
	return mapping, nil
}

func (vfs *VideoFrameService) downloadVideo(videoURL string, taskID uuid.UUID) (string, error) {
	resp, err := http.Get(videoURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	localPath := filepath.Join(vfs.frameDir, fmt.Sprintf("%s_source.mp4", taskID))
	out, err := os.Create(localPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return localPath, err
}

func (vfs *VideoFrameService) analyzeFrame(framePath string) (*ExtractedFrame, error) {
	file, err := os.Open(framePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	width := bounds.Max.X - bounds.Min.X
	height := bounds.Max.Y - bounds.Min.Y

	graySum := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			gray := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
			graySum += int(gray * 100)
		}
	}

	avgBrightness := float64(graySum) / float64(width*height) / 100.0

	isFlagged := avgBrightness < 0.1 || avgBrightness > 0.95
	score := 0.3
	violations := []string{}

	if isFlagged {
		score = 0.8
		if avgBrightness < 0.1 {
			violations = append(violations, "too_dark")
		} else {
			violations = append(violations, "too_bright")
		}
	}

	result := &ExtractedFrame{
		IsFlagged:  isFlagged,
		Score:      score,
		Violations: violations,
		Details: map[string]interface{}{
			"width":          width,
			"height":         height,
			"avg_brightness": avgBrightness,
		},
	}

	return result, nil
}

func (vfs *VideoFrameService) ExtractFrames(videoID uuid.UUID) error {
	var video models.Video
	if err := vfs.db.First(&video, videoID).Error; err != nil {
		return err
	}
	
	var task models.ModerationTask
	if err := vfs.db.Where("video_id = ?", videoID).
		Order("created_at DESC").
		First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("no moderation task found for video %s", videoID)
		}
		return err
	}
	
	return vfs.ExtractFramesForTask(context.Background(), task.ID, videoID)
}

func (vfs *VideoFrameService) GetFrameExtractionStatus(videoID uuid.UUID) string {
	var task models.ModerationTask
	if err := vfs.db.Where("video_id = ?", videoID).
		Order("created_at DESC").
		First(&task).Error; err != nil {
		return "not_started"
	}
	
	return vfs.GetFrameExtractionStatusForTask(task.ID)
}

func (vfs *VideoFrameService) GetVideoFrames(videoID uuid.UUID, onlyFlagged bool) ([]models.VideoFrame, error) {
	var task models.ModerationTask
	if err := vfs.db.Where("video_id = ?", videoID).
		Order("created_at DESC").
		First(&task).Error; err != nil {
		return nil, err
	}
	
	var frames []models.VideoFrame
	query := vfs.db.Where("task_id = ?", task.ID)
	
	if onlyFlagged {
		query = query.Where("is_flagged = ?", true)
	}
	
	if err := query.Order("frame_index ASC").Find(&frames).Error; err != nil {
		return nil, err
	}
	return frames, nil
}
