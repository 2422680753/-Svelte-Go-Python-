package services

import (
	"bytes"
	"content-moderation/config"
	"content-moderation/models"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
}

type FrameExtractionJob struct {
	VideoID   uuid.UUID
	VideoURL  string
	Duration  int
}

type FrameAnalysisResult struct {
	FrameIndex  int                    `json:"frame_index"`
	Timestamp   float64                `json:"timestamp"`
	IsFlagged   bool                   `json:"is_flagged"`
	Score        float64                `json:"score"`
	Violations   []string               `json:"violations"`
	Details      map[string]interface{}   `json:"details"`
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
	}
}

func (vfs *VideoFrameService) ExtractFrames(videoID uuid.UUID) error {
	var video models.Video
	if err := vfs.db.First(&video, videoID).Error; err != nil {
		return err
	}

	job := FrameExtractionJob{
		VideoID:  videoID,
		VideoURL: video.VideoURL,
		Duration: video.Duration,
	}

	vfs.workerPool <- struct{}{}
	go func() {
		defer func() { <-vfs.workerPool }()
		if err := vfs.processFrameExtraction(job); err != nil {
			log.Printf("Frame extraction failed for video %s: %v", videoID, err)
			vfs.updateFrameExtractionStatus(videoID, "failed")
		}
	}()

	return nil
}

func (vfs *VideoFrameService) processFrameExtraction(job FrameExtractionJob) error {
	vfs.updateFrameExtractionStatus(job.VideoID, "processing")

	localPath, err := vfs.downloadVideo(job.VideoURL, job.VideoID)
	if err != nil {
		return err
	}
	defer os.Remove(localPath)

	frameInterval := vfs.config.Moderation.VideoFrameInterval
	totalFrames := job.Duration / frameInterval
	if totalFrames < 1 {
		totalFrames = 1
	}

	var wg sync.WaitGroup
	results := make(chan FrameAnalysisResult, totalFrames)
	errors := make(chan error, totalFrames)

	for i := 0; i < totalFrames; i++ {
		wg.Add(1)
		go func(frameIndex int) {
			defer wg.Done()

			timestamp := float64(frameIndex * frameInterval)
			if timestamp >= float64(job.Duration) {
				timestamp = float64(job.Duration - 1)
			}

			framePath, err := vfs.extractSingleFrame(localPath, job.VideoID, frameIndex, timestamp)
			if err != nil {
				errors <- err
				return
			}
			defer os.Remove(framePath)

			analysis, err := vfs.analyzeFrame(framePath)
			if err != nil {
				errors <- err
				return
			}

			frameURL, err := vfs.uploadFrame(framePath, job.VideoID, frameIndex)
			if err != nil {
				errors <- err
				return
			}

			results <- FrameAnalysisResult{
				FrameIndex: frameIndex,
				Timestamp:  timestamp,
				IsFlagged:  analysis.IsFlagged,
				Score:      analysis.Score,
				Violations: analysis.Violations,
				Details:    analysis.Details,
			}

			videoFrame := &models.VideoFrame{
				VideoID:    job.VideoID,
				FrameIndex: frameIndex,
				Timestamp:  timestamp,
				FrameURL:   frameURL,
				Analysis:   analysis.Details,
				IsFlagged:  analysis.IsFlagged,
			}
			if err := vfs.db.Create(videoFrame).Error; err != nil {
				log.Printf("Failed to save frame %d for video %s: %v", frameIndex, job.VideoID, err)
			}

		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	flaggedFrames := make([]map[string]interface{}, 0)
	for result := range results {
		if result.IsFlagged {
			flaggedFrames = append(flaggedFrames, map[string]interface{}{
				"frame_index": result.FrameIndex,
				"timestamp":   result.Timestamp,
				"score":       result.Score,
				"violations":  result.Violations,
			})
		}
	}

	ctx := vfs.redis.Context()
	cacheKey := fmt.Sprintf("video:%s:flagged_frames", job.VideoID)
	flaggedData, _ := json.Marshal(flaggedFrames)
	vfs.redis.Set(ctx, cacheKey, flaggedData, 24*time.Hour)

	vfs.updateFrameExtractionStatus(job.VideoID, "completed")
	return nil
}

func (vfs *VideoFrameService) downloadVideo(videoURL string, videoID uuid.UUID) (string, error) {
	resp, err := http.Get(videoURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	localPath := filepath.Join(vfs.frameDir, fmt.Sprintf("%s.mp4", videoID))
	out, err := os.Create(localPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return localPath, err
}

func (vfs *VideoFrameService) extractSingleFrame(videoPath string, videoID uuid.UUID, frameIndex int, timestamp float64) (string, error) {
	framePath := filepath.Join(vfs.frameDir, fmt.Sprintf("%s_frame_%d.jpg", videoID, frameIndex))
	
	cmd := exec.Command("ffmpeg",
		"-y",
		"-ss", fmt.Sprintf("%.2f", timestamp),
		"-i", videoPath,
		"-vframes", "1",
		"-q:v", "2",
		framePath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	return framePath, nil
}

func (vfs *VideoFrameService) analyzeFrame(framePath string) (*FrameAnalysisResult, error) {
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

	result := &FrameAnalysisResult{
		IsFlagged:  avgBrightness < 0.1 || avgBrightness > 0.95,
		Score:      0.3,
		Violations: []string{},
		Details: map[string]interface{}{
			"width":         width,
			"height":        height,
			"avg_brightness": avgBrightness,
		},
	}

	return result, nil
}

func (vfs *VideoFrameService) uploadFrame(framePath string, videoID uuid.UUID, frameIndex int) (string, error) {
	return fmt.Sprintf("http://localhost:9000/frames/%s_%d.jpg", videoID, frameIndex), nil
}

func (vfs *VideoFrameService) updateFrameExtractionStatus(videoID uuid.UUID, status string) {
	ctx := vfs.redis.Context()
	cacheKey := fmt.Sprintf("video:%s:frame_status", videoID)
	vfs.redis.Set(ctx, cacheKey, status, 24*time.Hour)
}

func (vfs *VideoFrameService) GetFrameExtractionStatus(videoID uuid.UUID) string {
	ctx := vfs.redis.Context()
	cacheKey := fmt.Sprintf("video:%s:frame_status", videoID)
	status, _ := vfs.redis.Get(ctx, cacheKey).Result()
	if status == "" {
		return "not_started"
	}
	return status
}

func (vfs *VideoFrameService) GetVideoFrames(videoID uuid.UUID, onlyFlagged bool) ([]models.VideoFrame, error) {
	var frames []models.VideoFrame
	query := vfs.db.Where("video_id = ?", videoID)
	
	if onlyFlagged {
		query = query.Where("is_flagged = ?", true)
	}
	
	if err := query.Order("frame_index ASC").Find(&frames).Error; err != nil {
		return nil, err
	}
	return frames, nil
}
