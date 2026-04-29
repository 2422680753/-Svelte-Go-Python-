package services

import (
	"content-moderation/config"
	"content-moderation/models"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

type StatisticsService struct {
	db     *gorm.DB
	redis  *redis.Client
	config *config.Config
}

type DashboardStats struct {
	TotalVideos          int64   `json:"total_videos"`
	PendingReview        int64   `json:"pending_review"`
	InReview             int64   `json:"in_review"`
	AutoApproved         int64   `json:"auto_approved"`
	AutoRejected         int64   `json:"auto_rejected"`
	HumanApproved        int64   `json:"human_approved"`
	HumanRejected        int64   `json:"human_rejected"`
	Published            int64   `json:"published"`
	Banned               int64   `json:"banned"`
	AppealsPending       int64   `json:"appeals_pending"`
	AutoRejectionRate      float64 `json:"auto_rejection_rate"`
	HumanRejectionRate   float64 `json:"human_rejection_rate"`
	AverageReviewTime    float64 `json:"average_review_time"`
	SLARate              float64 `json:"sla_rate"`
}

type DailyTrendData struct {
	Date               string  `json:"date"`
	TotalVideos        int     `json:"total_videos"`
	AutoModerated      int     `json:"auto_moderated"`
	HumanReviewed      int     `json:"human_reviewed"`
	Approved           int     `json:"approved"`
	Rejected           int     `json:"rejected"`
}

func NewStatisticsService(db *gorm.DB, redis *redis.Client, cfg *config.Config) *StatisticsService {
	return &StatisticsService{
		db:     db,
		redis:  redis,
		config: cfg,
	}
}

func (ss *StatisticsService) GetDashboardStats() (*DashboardStats, error) {
	stats := &DashboardStats{}

	if err := ss.db.Model(&models.Video{}).Count(&stats.TotalVideos).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusNeedReview)).
		Count(&stats.PendingReview).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusInReview)).
		Count(&stats.InReview).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusAutoApproved)).
		Count(&stats.AutoApproved).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusAutoRejected)).
		Count(&stats.AutoRejected).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusHumanApproved)).
		Count(&stats.HumanApproved).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusHumanRejected)).
		Count(&stats.HumanRejected).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.Video{}).
		Where("is_published = ?", true).
		Count(&stats.Published).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.ModerationTask{}).
		Where("current_status = ?", string(models.StatusBanned)).
		Count(&stats.Banned).Error; err != nil {
		return nil, err
	}

	if err := ss.db.Model(&models.Appeal{}).
		Where("status IN ?", []string{string(models.StatusAppealed), "in_review"}).
		Count(&stats.AppealsPending).Error; err != nil {
		return nil, err
	}

	totalAuto := stats.AutoApproved + stats.AutoRejected
	if totalAuto > 0 {
		stats.AutoRejectionRate = float64(stats.AutoRejected) / float64(totalAuto) * 100
	}

	totalHuman := stats.HumanApproved + stats.HumanRejected
	if totalHuman > 0 {
		stats.HumanRejectionRate = float64(stats.HumanRejected) / float64(totalHuman) * 100
	}

	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	var recentStats []models.DailyStatistics
	if err := ss.db.Where("date >= ?", sevenDaysAgo).Find(&recentStats).Error; err != nil {
		return nil, err
	}

	var totalReviewTime float64
	var totalReviews int
	var totalSLAMet int64
	var totalSLATotal int64

	for _, s := range recentStats {
		totalReviewTime += s.AverageReviewTime * float64(s.HumanReviewed)
		totalReviews += s.HumanReviewed
		totalSLAMet += int64(s.SLAMet)
		totalSLATotal += int64(s.SLAMet + s.SLAMissed)
	}

	if totalReviews > 0 {
		stats.AverageReviewTime = totalReviewTime / float64(totalReviews)
	}

	if totalSLATotal > 0 {
		stats.SLARate = float64(totalSLAMet) / float64(totalSLATotal) * 100
	}

	return stats, nil
}

func (ss *StatisticsService) GetDailyTrends(days int) ([]DailyTrendData, error) {
	startDate := time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)
	
	var stats []models.DailyStatistics
	if err := ss.db.Where("date >= ?", startDate).
		Order("date ASC").
		Find(&stats).Error; err != nil {
		return nil, err
	}

	trends := make([]DailyTrendData, len(stats))
	for i, s := range stats {
		trends[i] = DailyTrendData{
			Date:          s.Date.Format("2006-01-02"),
			TotalVideos:   s.TotalVideos,
			AutoModerated: s.AutoModerated,
			HumanReviewed: s.HumanReviewed,
			Approved:      s.AutoApproved + s.HumanApproved,
			Rejected:      s.AutoRejected + s.HumanRejected,
		}
	}

	return trends, nil
}

func (ss *StatisticsService) GetReviewerPerformance(startDate, endDate time.Time) ([]models.ReviewerPerformance, error) {
	var performances []models.ReviewerPerformance
	if err := ss.db.Preload("Reviewer").
		Where("date >= ? AND date <= ?", startDate, endDate).
		Order("date DESC").
		Find(&performances).Error; err != nil {
		return nil, err
	}
	return performances, nil
}

func (ss *StatisticsService) GetViolationStatistics(startDate, endDate time.Time) (map[string]int, error) {
	type ViolationCount struct {
		Violation string
		Count     int64
	}

	var results []ViolationCount
	
	if err := ss.db.Raw(`
		SELECT jsonb_array_elements_text(violation_tags) as violation, COUNT(*) as count
		FROM human_moderation_results
		WHERE created_at >= ? AND created_at <= ? AND decision = 'reject'
		GROUP BY violation
		ORDER BY count DESC
	`, startDate, endDate).Scan(&results).Error; err != nil {
		return nil, err
	}

	stats := make(map[string]int)
	for _, r := range results {
		stats[r.Violation] = int(r.Count)
	}

	return stats, nil
}

func (ss *StatisticsService) GetSLAPerformance(days int) (map[string]interface{}, error) {
	startDate := time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)

	var totalMet, totalMissed int64
	
	if err := ss.db.Model(&models.DailyStatistics{}).
		Where("date >= ?", startDate).
		Select("SUM(sla_met) as total, SUM(sla_missed) as total_missed").
		Row().Scan(&totalMet, &totalMissed); err != nil {
		return nil, err
	}

	total := totalMet + totalMissed
	var rate := 0.0
	if total > 0 {
		rate = float64(totalMet) / float64(total) * 100
	}

	return map[string]interface{}{
		"sla_met":    totalMet,
		"sla_missed": totalMissed,
		"sla_rate":   rate,
		"period_days": days,
	}, nil
}
