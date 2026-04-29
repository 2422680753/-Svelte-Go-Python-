package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrOptimisticLockFailure = errors.New("optimistic lock failure: record was modified by another process")
	ErrTaskAlreadyLocked     = errors.New("task is already locked by another process")
	ErrVideoTaskMismatch     = errors.New("video ID does not match task's video ID")
	ErrStatusTransition      = errors.New("invalid status transition")
)

type BaseModel struct {
	ID        uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time      `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

type User struct {
	BaseModel
	Username     string    `gorm:"uniqueIndex:idx_users_username;not null;size:100" json:"username"`
	Email        string    `gorm:"uniqueIndex:idx_users_email;not null;size:255" json:"email"`
	PasswordHash string    `gorm:"not null;size:255" json:"-"`
	Role         string    `gorm:"not null;default:'reviewer';size:50;index:idx_users_role" json:"role"`
	Department   string    `gorm:"size:100" json:"department"`
	IsActive     bool      `gorm:"default:true;index" json:"is_active"`
	LastLoginAt  time.Time `json:"last_login_at"`
}

type Video struct {
	BaseModel
	Title         string    `gorm:"size:500" json:"title"`
	Description   string    `gorm:"type:text" json:"description"`
	VideoURL      string    `gorm:"not null;type:text;index:idx_videos_url" json:"video_url"`
	ThumbnailURL  string    `gorm:"type:text" json:"thumbnail_url"`
	Duration      int       `json:"duration"`
	FileSize      int64     `json:"file_size"`
	UploaderID    uuid.UUID `gorm:"not null;index:idx_videos_uploader" json:"uploader_id"`
	Uploader      User      `gorm:"foreignKey:UploaderID" json:"uploader"`
	Status        string    `gorm:"not null;default:'pending';size:50;index:idx_videos_status" json:"status"`
	IsPublished   bool      `gorm:"default:false;index" json:"is_published"`
	Version       int       `gorm:"not null;default:0" json:"-"`
}

func (v *Video) BeforeSave(tx *gorm.DB) error {
	if tx.Statement.Changed("Version") {
		oldVersion := v.Version
		v.Version++
		
		result := tx.Model(&Video{}).
			Where("id = ? AND version = ?", v.ID, oldVersion).
			Updates(map[string]interface{}{
				"status":         v.Status,
				"is_published":   v.IsPublished,
				"title":          v.Title,
				"description":    v.Description,
				"video_url":      v.VideoURL,
				"thumbnail_url":  v.ThumbnailURL,
				"duration":       v.Duration,
				"file_size":      v.FileSize,
				"version":        oldVersion + 1,
			})
		
		if result.Error != nil {
			return result.Error
		}
		
		if result.RowsAffected == 0 {
			return ErrOptimisticLockFailure
		}
	}
	return nil
}

type ViolationTag struct {
	BaseModel
	Name        string `gorm:"uniqueIndex:idx_violation_tags_name;not null;size:100" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	Category    string `gorm:"not null;size:50;index:idx_violation_tags_category" json:"category"`
	Severity    string `gorm:"not null;default:'medium';size:20" json:"severity"`
	IsActive    bool   `gorm:"default:true;index" json:"is_active"`
}

type ModerationTask struct {
	BaseModel
	VideoID          uuid.UUID  `gorm:"not null;uniqueIndex:idx_tasks_video;index:idx_tasks_video_status" json:"video_id"`
	Video            Video      `gorm:"foreignKey:VideoID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT" json:"video"`
	AssignedTo       *uuid.UUID `gorm:"index:idx_tasks_assigned" json:"assigned_to,omitempty"`
	Assignee         *User      `gorm:"foreignKey:AssignedTo" json:"assignee,omitempty"`
	CurrentStatus    string     `gorm:"not null;default:'pending';size:50;index:idx_tasks_status;index:idx_tasks_video_status" json:"current_status"`
	PreviousStatus   string     `gorm:"size:50" json:"previous_status"`
	Priority         string     `gorm:"not null;default:'normal';size:20;index:idx_tasks_priority" json:"priority"`
	SLADeadline      time.Time  `gorm:"index:idx_tasks_sla" json:"sla_deadline"`
	AutoModerationAt *time.Time `json:"auto_moderation_at,omitempty"`
	HumanReviewAt    *time.Time `json:"human_review_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	Version          int        `gorm:"not null;default:0" json:"-"`
}

func (mt *ModerationTask) BeforeSave(tx *gorm.DB) error {
	if tx.Statement.Changed("Version") {
		oldVersion := mt.Version
		mt.Version++
		
		result := tx.Model(&ModerationTask{}).
			Where("id = ? AND version = ?", mt.ID, oldVersion).
			Updates(map[string]interface{}{
				"assigned_to":        mt.AssignedTo,
				"current_status":     mt.CurrentStatus,
				"previous_status":    mt.PreviousStatus,
				"priority":           mt.Priority,
				"sla_deadline":       mt.SLADeadline,
				"auto_moderation_at": mt.AutoModerationAt,
				"human_review_at":    mt.HumanReviewAt,
				"completed_at":       mt.CompletedAt,
				"version":            oldVersion + 1,
			})
		
		if result.Error != nil {
			return result.Error
		}
		
		if result.RowsAffected == 0 {
			return ErrOptimisticLockFailure
		}
	}
	return nil
}

func (mt *ModerationTask) ValidateVideoID(videoID uuid.UUID) error {
	if mt.VideoID != videoID {
		return fmt.Errorf("%w: task expects video %s but got %s", ErrVideoTaskMismatch, mt.VideoID, videoID)
	}
	return nil
}

type AutoModerationResult struct {
	BaseModel
	TaskID           uuid.UUID `gorm:"not null;uniqueIndex:idx_auto_results_task;index:idx_auto_results_task_status" json:"task_id"`
	Task             ModerationTask `gorm:"foreignKey:TaskID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"task"`
	OverallScore     float64   `gorm:"not null;index:idx_auto_results_score" json:"overall_score"`
	ViolationScores  JSON      `gorm:"type:jsonb" json:"violation_scores"`
	FlaggedFrames    JSON      `gorm:"type:jsonb" json:"flagged_frames"`
	TextAnalysis     JSON      `gorm:"type:jsonb" json:"text_analysis"`
	AudioAnalysis    JSON      `gorm:"type:jsonb" json:"audio_analysis"`
	Recommendation   string    `gorm:"not null;size:20;index" json:"recommendation"`
	ProcessingTime   float64   `json:"processing_time"`
	VideoID          uuid.UUID `gorm:"not null;index" json:"video_id"`
}

func (amr *AutoModerationResult) BeforeCreate(tx *gorm.DB) error {
	var task ModerationTask
	if err := tx.First(&task, amr.TaskID).Error; err != nil {
		return err
	}
	
	amr.VideoID = task.VideoID
	return nil
}

type HumanModerationResult struct {
	BaseModel
	TaskID          uuid.UUID  `gorm:"not null;index:idx_human_results_task" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"task"`
	ReviewerID      uuid.UUID  `gorm:"not null;index:idx_human_results_reviewer" json:"reviewer_id"`
	Reviewer        User       `gorm:"foreignKey:ReviewerID" json:"reviewer"`
	Decision        string     `gorm:"not null;size:20;index" json:"decision"`
	ViolationTags   JSON       `gorm:"type:jsonb" json:"violation_tags"`
	Comment         string     `gorm:"type:text" json:"comment"`
	ReviewDuration  float64    `json:"review_duration"`
	IsAppeal        bool       `gorm:"default:false;index" json:"is_appeal"`
	AppealID        *uuid.UUID `gorm:"index" json:"appeal_id,omitempty"`
	VideoID         uuid.UUID  `gorm:"not null;index" json:"video_id"`
}

func (hmr *HumanModerationResult) BeforeCreate(tx *gorm.DB) error {
	var task ModerationTask
	if err := tx.First(&task, hmr.TaskID).Error; err != nil {
		return err
	}
	
	hmr.VideoID = task.VideoID
	return nil
}

type VideoFrame struct {
	BaseModel
	TaskID         uuid.UUID `gorm:"not null;uniqueIndex:idx_frames_task_index,priority:1;index:idx_frames_task" json:"task_id"`
	VideoID        uuid.UUID `gorm:"not null;index:idx_frames_video" json:"video_id"`
	Video          Video     `gorm:"foreignKey:VideoID" json:"video"`
	FrameIndex     int       `gorm:"not null;uniqueIndex:idx_frames_task_index,priority:2" json:"frame_index"`
	Timestamp      float64   `gorm:"not null;index:idx_frames_timestamp" json:"timestamp"`
	FrameURL       string    `gorm:"not null;type:text" json:"frame_url"`
	Analysis       JSON      `gorm:"type:jsonb" json:"analysis"`
	IsFlagged      bool      `gorm:"default:false;index:idx_frames_flagged" json:"is_flagged"`
	OriginalIndex  int       `json:"-"`
}

func (vf *VideoFrame) BeforeCreate(tx *gorm.DB) error {
	vf.OriginalIndex = vf.FrameIndex
	return nil
}

type FrameSegment struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TaskID       uuid.UUID `gorm:"not null;index" json:"task_id"`
	StartIndex   int       `gorm:"not null" json:"start_index"`
	EndIndex     int       `gorm:"not null" json:"end_index"`
	StartTime    float64   `gorm:"not null" json:"start_time"`
	EndTime      float64   `gorm:"not null" json:"end_time"`
	FrameCount   int       `gorm:"not null" json:"frame_count"`
	IsLoaded     bool      `gorm:"default:false" json:"is_loaded"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type Appeal struct {
	BaseModel
	TaskID          uuid.UUID `gorm:"not null;index:idx_appeals_task" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID" json:"task"`
	VideoID         uuid.UUID `gorm:"not null;index:idx_appeals_video" json:"video_id"`
	Video           Video     `gorm:"foreignKey:VideoID" json:"video"`
	OriginalDecision string   `gorm:"not null;size:50" json:"original_decision"`
	Reason          string    `gorm:"not null;type:text" json:"reason"`
	SubmittedBy     uuid.UUID `gorm:"not null;index:idx_appeals_submitter" json:"submitted_by"`
	Submitter       User      `gorm:"foreignKey:SubmittedBy" json:"submitter"`
	Status          string    `gorm:"not null;default:'pending';size:50;index:idx_appeals_status" json:"status"`
	AssignedTo      *uuid.UUID `gorm:"index:idx_appeals_assigned" json:"assigned_to,omitempty"`
	Assignee        *User      `gorm:"foreignKey:AssignedTo" json:"assignee,omitempty"`
	AppealResult    string    `gorm:"size:20" json:"appeal_result"`
	AppealComment   string    `gorm:"type:text" json:"appeal_comment"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	Version         int        `gorm:"not null;default:0" json:"-"`
}

func (a *Appeal) BeforeSave(tx *gorm.DB) error {
	if tx.Statement.Changed("Version") {
		oldVersion := a.Version
		a.Version++
		
		result := tx.Model(&Appeal{}).
			Where("id = ? AND version = ?", a.ID, oldVersion).
			Updates(map[string]interface{}{
				"status":         a.Status,
				"assigned_to":    a.AssignedTo,
				"appeal_result":  a.AppealResult,
				"appeal_comment": a.AppealComment,
				"resolved_at":    a.ResolvedAt,
				"version":        oldVersion + 1,
			})
		
		if result.Error != nil {
			return result.Error
		}
		
		if result.RowsAffected == 0 {
			return ErrOptimisticLockFailure
		}
	}
	return nil
}

type ModerationLog struct {
	BaseModel
	TaskID          uuid.UUID  `gorm:"not null;index:idx_logs_task" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID" json:"task"`
	VideoID         uuid.UUID  `gorm:"not null;index:idx_logs_video" json:"video_id"`
	Video           Video      `gorm:"foreignKey:VideoID" json:"video"`
	ActorType       string     `gorm:"not null;size:20" json:"actor_type"`
	ActorID         *uuid.UUID `gorm:"index:idx_logs_actor" json:"actor_id,omitempty"`
	Actor           *User      `gorm:"foreignKey:ActorID" json:"actor,omitempty"`
	Action          string     `gorm:"not null;size:50;index:idx_logs_action" json:"action"`
	FromStatus      string     `gorm:"size:50" json:"from_status"`
	ToStatus        string     `gorm:"size:50" json:"to_status"`
	Details         JSON       `gorm:"type:jsonb" json:"details"`
	Comment         string     `gorm:"type:text" json:"comment"`
}

type BatchOperation struct {
	BaseModel
	OperatorID      uuid.UUID `gorm:"not null;index:idx_batch_operator" json:"operator_id"`
	Operator        User      `gorm:"foreignKey:OperatorID" json:"operator"`
	OperationType   string    `gorm:"not null;size:50;index:idx_batch_type" json:"operation_type"`
	TaskIDs         JSON      `gorm:"type:jsonb" json:"task_ids"`
	TotalCount      int       `gorm:"not null" json:"total_count"`
	SuccessCount    int       `json:"success_count"`
	FailedCount     int       `json:"failed_count"`
	Status          string    `gorm:"not null;default:'processing';size:20;index" json:"status"`
	Parameters      JSON      `gorm:"type:jsonb" json:"parameters"`
	ErrorDetails    JSON      `gorm:"type:jsonb" json:"error_details"`
}

type DailyStatistics struct {
	BaseModel
	Date               time.Time `gorm:"uniqueIndex:idx_daily_stats_date;not null;index" json:"date"`
	TotalVideos        int       `json:"total_videos"`
	AutoModerated      int       `json:"auto_moderated"`
	HumanReviewed      int       `json:"human_reviewed"`
	AutoApproved       int       `json:"auto_approved"`
	AutoRejected       int       `json:"auto_rejected"`
	NeedReview         int       `json:"need_review"`
	HumanApproved      int       `json:"human_approved"`
	HumanRejected      int       `json:"human_rejected"`
	AverageReviewTime  float64   `json:"average_review_time"`
	SLAMet             int       `json:"sla_met"`
	SLAMissed          int       `json:"sla_missed"`
	AppealsReceived    int       `json:"appeals_received"`
	AppealsResolved    int       `json:"appeals_resolved"`
}

type ReviewerPerformance struct {
	BaseModel
	ReviewerID          uuid.UUID `gorm:"not null;index:idx_perf_reviewer" json:"reviewer_id"`
	Reviewer            User      `gorm:"foreignKey:ReviewerID" json:"reviewer"`
	Date                time.Time `gorm:"not null;index:idx_perf_date" json:"date"`
	TotalReviews        int       `json:"total_reviews"`
	ApprovedCount       int       `json:"approved_count"`
	RejectedCount       int       `json:"rejected_count"`
	AverageReviewTime   float64   `json:"average_review_time"`
	AppealsUpholdCount  int       `json:"appeals_uphold_count"`
	AppealsOverturnCount int      `json:"appeals_overturn_count"`
	AccuracyScore       float64   `json:"accuracy_score"`
}

type TaskLockRecord struct {
	BaseModel
	TaskID     uuid.UUID `gorm:"not null;uniqueIndex:idx_task_locks_task" json:"task_id"`
	VideoID    uuid.UUID `gorm:"not null" json:"video_id"`
	LockHolder uuid.UUID `gorm:"not null;index:idx_task_locks_holder" json:"lock_holder"`
	HolderType string    `gorm:"not null;size:20" json:"holder_type"`
	LockedAt   time.Time `gorm:"not null;index" json:"locked_at"`
	ExpiresAt  time.Time `gorm:"not null;index" json:"expires_at"`
	IsActive   bool      `gorm:"default:true;index" json:"is_active"`
}

type FrameCache struct {
	BaseModel
	TaskID       uuid.UUID `gorm:"not null;index:idx_frame_cache_task" json:"task_id"`
	VideoID      uuid.UUID `gorm:"not null" json:"video_id"`
	FrameIndex   int       `gorm:"not null" json:"frame_index"`
	Timestamp    float64   `gorm:"not null" json:"timestamp"`
	FrameData    []byte    `gorm:"type:bytea" json:"-"`
	Analysis     JSON      `gorm:"type:jsonb" json:"analysis"`
	IsFlagged    bool      `json:"is_flagged"`
	LastAccessed time.Time `gorm:"index" json:"last_accessed"`
	AccessCount  int       `gorm:"default:0" json:"access_count"`
}

type JSON map[string]interface{}

func (j JSON) GormDataType() string {
	return "jsonb"
}
