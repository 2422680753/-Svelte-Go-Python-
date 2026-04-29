package main

import (
	"content-moderation/config"
	"content-moderation/handlers"
	"content-moderation/models"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg := config.LoadConfig()

	gin.SetMode(cfg.Server.Mode)
	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	dsn := "host=" + cfg.Database.Host +
		" user=" + cfg.Database.User +
		" password=" + cfg.Database.Password +
		" dbname=" + cfg.Database.DBName +
		" port=" + cfg.Database.Port +
		" sslmode=" + cfg.Database.SSLMode

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	err = db.AutoMigrate(
		&models.User{},
		&models.Video{},
		&models.ViolationTag{},
		&models.ModerationTask{},
		&models.AutoModerationResult{},
		&models.HumanModerationResult{},
		&models.VideoFrame{},
		&models.Appeal{},
		&models.ModerationLog{},
		&models.BatchOperation{},
		&models.DailyStatistics{},
		&models.ReviewerPerformance{},
	)
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	stateMachine := models.NewStateMachine(db, cfg)

	authHandler := handlers.NewAuthHandler(db, cfg)
	taskHandler := handlers.NewTaskHandler(db, redisClient, cfg, stateMachine)
	appealHandler := handlers.NewAppealHandler(db, redisClient, cfg, stateMachine)
	statsHandler := handlers.NewStatsHandler(db, redisClient, cfg)

	api := r.Group("/api/v1")
	{
		auth := api.Group("/auth")
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/login", authHandler.Login)
			auth.GET("/me", authHandler.AuthMiddleware(), authHandler.GetCurrentUser)
		}

		protected := api.Group("")
		protected.Use(authHandler.AuthMiddleware())
		{
			videos := protected.Group("/videos")
			{
				videos.POST("", taskHandler.CreateVideoAndTask)
			}

			tasks := protected.Group("/tasks")
			{
				tasks.GET("", taskHandler.GetTasks)
				tasks.GET("/my-pending", taskHandler.GetMyPendingTasks)
				tasks.GET("/my-history", taskHandler.GetMyReviewHistory)
				tasks.GET("/:id", taskHandler.GetTask)
				tasks.POST("/:id/start-review", taskHandler.StartReview)
				tasks.POST("/:id/submit-review", taskHandler.SubmitReview)
				tasks.POST("/:id/assign", authHandler.RequireRole("admin", "senior_reviewer"), taskHandler.AssignTask)
			}

			batch := protected.Group("/batch")
			batch.Use(authHandler.RequireRole("admin", "senior_reviewer"))
			{
				batch.POST("", taskHandler.CreateBatchOperation)
				batch.GET("/:id", taskHandler.GetBatchOperation)
			}

			appeals := protected.Group("/appeals")
			{
				appeals.POST("", appealHandler.SubmitAppeal)
				appeals.GET("/my", appealHandler.GetMyAppeals)
				appeals.GET("/pending", authHandler.RequireRole("admin", "senior_reviewer"), appealHandler.GetPendingAppeals)
				appeals.GET("/:id", authHandler.RequireRole("admin", "senior_reviewer"), appealHandler.GetAppealDetails)
				appeals.POST("/:id/assign", authHandler.RequireRole("admin", "senior_reviewer"), appealHandler.AssignAppeal)
				appeals.POST("/:id/resolve", authHandler.RequireRole("admin", "senior_reviewer"), appealHandler.ResolveAppeal)
			}

			stats := protected.Group("/stats")
			stats.Use(authHandler.RequireRole("admin", "senior_reviewer", "reviewer"))
			{
				stats.GET("/dashboard", statsHandler.GetDashboardStats)
				stats.GET("/trends", statsHandler.GetDailyTrends)
				stats.GET("/reviewers", statsHandler.GetReviewerPerformance)
				stats.GET("/violations", statsHandler.GetViolationStatistics)
				stats.GET("/sla", statsHandler.GetSLAPerformance)
			}

			tags := protected.Group("/violation-tags")
			{
				tags.GET("", func(c *gin.Context) {
					var tags []models.ViolationTag
					if err := db.Where("is_active = ?", true).Order("category, name").Find(&tags).Error; err != nil {
						c.JSON(500, gin.H{"error": "Failed to get violation tags"})
						return
					}
					c.JSON(200, tags)
				})
			}
		}
	}

	seedViolationTags(db)

	log.Printf("Server starting on port %s", cfg.Server.Port)
	if err := r.Run(":" + cfg.Server.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func seedViolationTags(db *gorm.DB) {
	var count int64
	db.Model(&models.ViolationTag{}).Count(&count)
	if count > 0 {
		return
	}

	tags := []models.ViolationTag{
		{Name: "暴力血腥", Description: "包含暴力、血腥、打斗等内容", Category: "暴力", Severity: "high"},
		{Name: "色情低俗", Description: "包含色情、低俗、不雅内容", Category: "色情", Severity: "high"},
		{Name: "恐怖惊悚", Description: "包含恐怖、惊悚、鬼怪等内容", Category: "恐怖", Severity: "medium"},
		{Name: "政治敏感", Description: "涉及政治敏感话题或人物", Category: "政治", Severity: "high"},
		{Name: "宗教敏感", Description: "涉及宗教敏感话题或冒犯内容", Category: "宗教", Severity: "high"},
		{Name: "仇恨言论", Description: "包含种族歧视、仇恨煽动等言论", Category: "仇恨", Severity: "high"},
		{Name: "虚假信息", Description: "传播虚假、误导性信息", Category: "虚假", Severity: "medium"},
		{Name: "赌博相关", Description: "涉及赌博、彩票等违规内容", Category: "赌博", Severity: "high"},
		{Name: "毒品相关", Description: "涉及毒品、药品滥用等内容", Category: "毒品", Severity: "high"},
		{Name: "未成年人不良", Description: "涉及未成年人不良行为或内容", Category: "未成年人", Severity: "high"},
		{Name: "侵犯版权", Description: "侵犯他人版权或知识产权", Category: "版权", Severity: "medium"},
		{Name: "侵犯隐私", Description: "侵犯他人隐私或个人信息", Category: "隐私", Severity: "medium"},
		{Name: "垃圾广告", Description: "包含垃圾广告、营销推广内容", Category: "广告", Severity: "low"},
		{Name: "其他违规", Description: "其他违反平台规则的内容", Category: "其他", Severity: "medium"},
	}

	for _, tag := range tags {
		db.Create(&tag)
	}
}
