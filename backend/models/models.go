package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BaseModel struct {
	ID        uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

type User struct {
	BaseModel
	Username     string    `gorm:"uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"not null;default:'reviewer'" json:"role"`
	Department   string    `json:"department"`
	IsActive     bool      `gorm:"default:true" json:"is_active"`
	LastLoginAt  time.Time `json:"last_login_at"`
}

type Video struct {
	BaseModel
	Title       string    `json:"title"`
	Description string    `json:"description"`
	VideoURL    string    `gorm:"not null" json:"video_url"`
	ThumbnailURL string   `json:"thumbnail_url"`
	Duration    int       `json:"duration"`
	FileSize    int64     `json:"file_size"`
	UploaderID  uuid.UUID `gorm:"not null" json:"uploader_id"`
	Uploader    User      `gorm:"foreignKey:UploaderID" json:"uploader"`
	Status      string    `gorm:"not null;default:'pending'" json:"status"`
	IsPublished bool      `gorm:"default:false" json:"is_published"`
}

type ViolationTag struct {
	BaseModel
	Name        string `gorm:"uniqueIndex;not null" json:"name"`
	Description string `json:"description"`
	Category    string `gorm:"not null" json:"category"`
	Severity    string `gorm:"not null;default:'medium'" json:"severity"`
	IsActive    bool   `gorm:"default:true" json:"is_active"`
}

type ModerationTask struct {
	BaseModel
	VideoID          uuid.UUID  `gorm:"not null;index" json:"video_id"`
	Video            Video      `gorm:"foreignKey:VideoID" json:"video"`
	AssignedTo       *uuid.UUID `gorm:"index" json:"assigned_to,omitempty"`
	Assignee         *User      `gorm:"foreignKey:AssignedTo" json:"assignee,omitempty"`
	CurrentStatus    string     `gorm:"not null;default:'pending'" json:"current_status"`
	PreviousStatus   string     `json:"previous_status"`
	Priority         string     `gorm:"not null;default:'normal'" json:"priority"`
	SLADeadline      time.Time  `json:"sla_deadline"`
	AutoModerationAt *time.Time `json:"auto_moderation_at,omitempty"`
	HumanReviewAt    *time.Time `json:"human_review_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type AutoModerationResult struct {
	BaseModel
	TaskID          uuid.UUID `gorm:"not null;index" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID" json:"task"`
	OverallScore    float64   `gorm:"not null" json:"overall_score"`
	ViolationScores JSON      `gorm:"type:jsonb" json:"violation_scores"`
	FlaggedFrames   JSON      `gorm:"type:jsonb" json:"flagged_frames"`
	TextAnalysis    JSON      `gorm:"type:jsonb" json:"text_analysis"`
	AudioAnalysis   JSON      `gorm:"type:jsonb" json:"audio_analysis"`
	Recommendation  string    `gorm:"not null" json:"recommendation"`
	ProcessingTime  float64   `json:"processing_time"`
}

type HumanModerationResult struct {
	BaseModel
	TaskID          uuid.UUID  `gorm:"not null;index" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID" json:"task"`
	ReviewerID      uuid.UUID  `gorm:"not null" json:"reviewer_id"`
	Reviewer        User       `gorm:"foreignKey:ReviewerID" json:"reviewer"`
	Decision        string     `gorm:"not null" json:"decision"`
	ViolationTags   JSON       `gorm:"type:jsonb" json:"violation_tags"`
	Comment         string     `json:"comment"`
	ReviewDuration  float64    `json:"review_duration"`
	IsAppeal        bool       `gorm:"default:false" json:"is_appeal"`
	AppealID        *uuid.UUID `json:"appeal_id,omitempty"`
}

type VideoFrame struct {
	BaseModel
	VideoID     uuid.UUID `gorm:"not null;index" json:"video_id"`
	Video       Video     `gorm:"foreignKey:VideoID" json:"video"`
	FrameIndex  int       `gorm:"not null" json:"frame_index"`
	Timestamp   float64   `gorm:"not null" json:"timestamp"`
	FrameURL    string    `gorm:"not null" json:"frame_url"`
	Analysis    JSON      `gorm:"type:jsonb" json:"analysis"`
	IsFlagged   bool      `gorm:"default:false" json:"is_flagged"`
}

type Appeal struct {
	BaseModel
	TaskID          uuid.UUID `gorm:"not null;index" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID" json:"task"`
	VideoID         uuid.UUID `gorm:"not null;index" json:"video_id"`
	Video           Video     `gorm:"foreignKey:VideoID" json:"video"`
	OriginalDecision string    `gorm:"not null" json:"original_decision"`
	Reason          string    `gorm:"not null" json:"reason"`
	SubmittedBy     uuid.UUID `gorm:"not null" json:"submitted_by"`
	Submitter       User      `gorm:"foreignKey:SubmittedBy" json:"submitter"`
	Status          string    `gorm:"not null;default:'pending'" json:"status"`
	AssignedTo      *uuid.UUID `gorm:"index" json:"assigned_to,omitempty"`
	Assignee        *User      `gorm:"foreignKey:AssignedTo" json:"assignee,omitempty"`
	AppealResult    string    `json:"appeal_result"`
	AppealComment   string    `json:"appeal_comment"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

type ModerationLog struct {
	BaseModel
	TaskID          uuid.UUID  `gorm:"not null;index" json:"task_id"`
	Task            ModerationTask `gorm:"foreignKey:TaskID" json:"task"`
	VideoID         uuid.UUID  `gorm:"not null;index" json:"video_id"`
	Video           Video      `gorm:"foreignKey:VideoID" json:"video"`
	ActorType       string     `gorm:"not null" json:"actor_type"`
	ActorID         *uuid.UUID `json:"actor_id,omitempty"`
	Actor           *User      `gorm:"foreignKey:ActorID" json:"actor,omitempty"`
	Action          string     `gorm:"not null" json:"action"`
	FromStatus      string     `json:"from_status"`
	ToStatus        string     `json:"to_status"`
	Details         JSON       `gorm:"type:jsonb" json:"details"`
	Comment         string     `json:"comment"`
}

type BatchOperation struct {
	BaseModel
	OperatorID      uuid.UUID `gorm:"not null" json:"operator_id"`
	Operator        User      `gorm:"foreignKey:OperatorID" json:"operator"`
	OperationType   string    `gorm:"not null" json:"operation_type"`
	TaskIDs         JSON      `gorm:"type:jsonb" json:"task_ids"`
	TotalCount      int       `gorm:"not null" json:"total_count"`
	SuccessCount    int       `json:"success_count"`
	FailedCount     int       `json:"failed_count"`
	Status          string    `gorm:"not null;default:'processing'" json:"status"`
	Parameters      JSON      `gorm:"type:jsonb" json:"parameters"`
	ErrorDetails    JSON      `gorm:"type:jsonb" json:"error_details"`
}

type DailyStatistics struct {
	BaseModel
	Date               time.Time `gorm:"uniqueIndex;not null" json:"date"`
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
	ReviewerID          uuid.UUID `gorm:"not null;index" json:"reviewer_id"`
	Reviewer            User      `gorm:"foreignKey:ReviewerID" json:"reviewer"`
	Date                time.Time `gorm:"not null;index" json:"date"`
	TotalReviews        int       `json:"total_reviews"`
	ApprovedCount       int       `json:"approved_count"`
	RejectedCount       int       `json:"rejected_count"`
	AverageReviewTime   float64   `json:"average_review_time"`
	AppealsUpholdCount  int       `json:"appeals_uphold_count"`
	AppealsOverturnCount int      `json:"appeals_overturn_count"`
	AccuracyScore       float64   `json:"accuracy_score"`
}

type JSON map[string]interface{}
