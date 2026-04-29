package handlers

import (
	"content-moderation/config"
	"content-moderation/services"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

type StatsHandler struct {
	db               *gorm.DB
	redis            *redis.Client
	config           *config.Config
	statisticsService *services.StatisticsService
}

func NewStatsHandler(db *gorm.DB, redis *redis.Client, cfg *config.Config) *StatsHandler {
	return &StatsHandler{
		db:                db,
		redis:             redis,
		config:            cfg,
		statisticsService: services.NewStatisticsService(db, redis, cfg),
	}
}

func (h *StatsHandler) GetDashboardStats(c *gin.Context) {
	stats, err := h.statisticsService.GetDashboardStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get dashboard stats"})
		return
	}

	c.JSON(http.StatusOK, stats)
}

func (h *StatsHandler) GetDailyTrends(c *gin.Context) {
	days := 30
	if c.Query("days") != "" {
		fmt.Sscanf(c.Query("days"), "%d", &days)
	}

	if days < 1 || days > 365 {
		days = 30
	}

	trends, err := h.statisticsService.GetDailyTrends(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get daily trends"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"period_days": days,
		"trends":      trends,
	})
}

func (h *StatsHandler) GetReviewerPerformance(c *gin.Context) {
	endDate := time.Now().Truncate(24 * time.Hour)
	startDate := endDate.AddDate(0, 0, -30)

	if c.Query("start_date") != "" {
		parsed, err := time.Parse("2006-01-02", c.Query("start_date"))
		if err == nil {
			startDate = parsed
		}
	}

	if c.Query("end_date") != "" {
		parsed, err := time.Parse("2006-01-02", c.Query("end_date"))
		if err == nil {
			endDate = parsed
		}
	}

	performances, err := h.statisticsService.GetReviewerPerformance(startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get reviewer performance"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"start_date":    startDate.Format("2006-01-02"),
		"end_date":      endDate.Format("2006-01-02"),
		"performances":  performances,
	})
}

func (h *StatsHandler) GetViolationStatistics(c *gin.Context) {
	endDate := time.Now().Truncate(24 * time.Hour)
	startDate := endDate.AddDate(0, 0, -30)

	if c.Query("start_date") != "" {
		parsed, err := time.Parse("2006-01-02", c.Query("start_date"))
		if err == nil {
			startDate = parsed
		}
	}

	if c.Query("end_date") != "" {
		parsed, err := time.Parse("2006-01-02", c.Query("end_date"))
		if err == nil {
			endDate = parsed
		}
	}

	stats, err := h.statisticsService.GetViolationStatistics(startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get violation statistics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"start_date": startDate.Format("2006-01-02"),
		"end_date":   endDate.Format("2006-01-02"),
		"violations": stats,
	})
}

func (h *StatsHandler) GetSLAPerformance(c *gin.Context) {
	days := 30
	if c.Query("days") != "" {
		fmt.Sscanf(c.Query("days"), "%d", &days)
	}

	if days < 1 || days > 365 {
		days = 30
	}

	performance, err := h.statisticsService.GetSLAPerformance(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get SLA performance"})
		return
	}

	c.JSON(http.StatusOK, performance)
}
