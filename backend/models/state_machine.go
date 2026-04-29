package models

import (
	"errors"
	"fmt"
	"time"

	"content-moderation/config"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ModerationStatus string

const (
	StatusPending            ModerationStatus = "pending"
	StatusAutoModerating     ModerationStatus = "auto_moderating"
	StatusAutoApproved       ModerationStatus = "auto_approved"
	StatusAutoRejected       ModerationStatus = "auto_rejected"
	StatusNeedReview         ModerationStatus = "need_review"
	StatusAssigned           ModerationStatus = "assigned"
	StatusInReview           ModerationStatus = "in_review"
	StatusHumanApproved      ModerationStatus = "human_approved"
	StatusHumanRejected      ModerationStatus = "human_rejected"
	StatusAppealed           ModerationStatus = "appealed"
	StatusAppealApproved     ModerationStatus = "appeal_approved"
	StatusAppealRejected     ModerationStatus = "appeal_rejected"
	StatusPublished          ModerationStatus = "published"
	StatusBanned             ModerationStatus = "banned"
)

type StateTransition struct {
	From      ModerationStatus
	To        ModerationStatus
	Action    string
	Condition func(task *ModerationTask, data map[string]interface{}) bool
}

type StateMachine struct {
	db     *gorm.DB
	config *config.Config
	transitions []StateTransition
}

func NewStateMachine(db *gorm.DB, cfg *config.Config) *StateMachine {
	sm := &StateMachine{
		db:     db,
		config: cfg,
	}
	sm.initTransitions()
	return sm
}

func (sm *StateMachine) initTransitions() {
	sm.transitions = []StateTransition{
		{
			From:   StatusPending,
			To:     StatusAutoModerating,
			Action: "start_auto_moderation",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return task.AssignedTo == nil
			},
		},
		{
			From:   StatusAutoModerating,
			To:     StatusAutoApproved,
			Action: "auto_approve",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				score, ok := data["auto_score"].(float64)
				return ok && score <= sm.config.Moderation.AutoApproveThreshold
			},
		},
		{
			From:   StatusAutoModerating,
			To:     StatusAutoRejected,
			Action: "auto_reject",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				score, ok := data["auto_score"].(float64)
				return ok && score >= sm.config.Moderation.AutoRejectThreshold
			},
		},
		{
			From:   StatusAutoModerating,
			To:     StatusNeedReview,
			Action: "need_review",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				score, ok := data["auto_score"].(float64)
				return ok && score > sm.config.Moderation.AutoApproveThreshold && 
					   score < sm.config.Moderation.AutoRejectThreshold
			},
		},
		{
			From:   StatusNeedReview,
			To:     StatusAssigned,
			Action: "assign_reviewer",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				_, ok := data["reviewer_id"].(string)
				return ok
			},
		},
		{
			From:   StatusAssigned,
			To:     StatusInReview,
			Action: "start_review",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return task.AssignedTo != nil
			},
		},
		{
			From:   StatusInReview,
			To:     StatusHumanApproved,
			Action: "human_approve",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				decision, ok := data["decision"].(string)
				return ok && decision == "approve"
			},
		},
		{
			From:   StatusInReview,
			To:     StatusHumanRejected,
			Action: "human_reject",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				decision, ok := data["decision"].(string)
				return ok && decision == "reject"
			},
		},
		{
			From:   StatusAutoApproved,
			To:     StatusPublished,
			Action: "publish",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusHumanApproved,
			To:     StatusPublished,
			Action: "publish",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusAutoRejected,
			To:     StatusBanned,
			Action: "ban",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusHumanRejected,
			To:     StatusBanned,
			Action: "ban",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
		{
			From:   StatusAutoRejected,
			To:     StatusAppealed,
			Action: "submit_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				_, ok := data["appeal_reason"].(string)
				return ok
			},
		},
		{
			From:   StatusHumanRejected,
			To:     StatusAppealed,
			Action: "submit_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				_, ok := data["appeal_reason"].(string)
				return ok
			},
		},
		{
			From:   StatusAppealed,
			To:     StatusAppealApproved,
			Action: "approve_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				result, ok := data["appeal_result"].(string)
				return ok && result == "approve"
			},
		},
		{
			From:   StatusAppealed,
			To:     StatusAppealRejected,
			Action: "reject_appeal",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				result, ok := data["appeal_result"].(string)
				return ok && result == "reject"
			},
		},
		{
			From:   StatusAppealApproved,
			To:     StatusNeedReview,
			Action: "reassign_review",
			Condition: func(task *ModerationTask, data map[string]interface{}) bool {
				return true
			},
		},
	}
}

func (sm *StateMachine) CanTransition(task *ModerationTask, to ModerationStatus, data map[string]interface{}) bool {
	for _, t := range sm.transitions {
		if ModerationStatus(task.CurrentStatus) == t.From && to == t.To {
			if t.Condition != nil {
				return t.Condition(task, data)
			}
			return true
		}
	}
	return false
}

func (sm *StateMachine) Transition(task *ModerationTask, to ModerationStatus, data map[string]interface{}, actorID *uuid.UUID, actorType string) error {
	if !sm.CanTransition(task, to, data) {
		return errors.New(fmt.Sprintf("invalid state transition from %s to %s", task.CurrentStatus, to))
	}

	return sm.db.Transaction(func(tx *gorm.DB) error {
		oldStatus := task.CurrentStatus
		task.PreviousStatus = oldStatus
		task.CurrentStatus = string(to)

		if err := tx.Save(task).Error; err != nil {
			return err
		}

		video := &Video{}
		if err := tx.First(video, task.VideoID).Error; err != nil {
			return err
		}

		video.Status = string(to)
		if to == StatusPublished {
			video.IsPublished = true
		} else if to == StatusBanned {
			video.IsPublished = false
		}
		if err := tx.Save(video).Error; err != nil {
			return err
		}

		var action string
		for _, t := range sm.transitions {
			if ModerationStatus(oldStatus) == t.From && to == t.To {
				action = t.Action
				break
			}
		}

		log := &ModerationLog{
			TaskID:     task.ID,
			VideoID:    task.VideoID,
			ActorType:  actorType,
			ActorID:    actorID,
			Action:     action,
			FromStatus: oldStatus,
			ToStatus:   string(to),
			Details:    data,
		}
		if err := tx.Create(log).Error; err != nil {
			return err
		}

		return nil
	})
}

func (sm *StateMachine) GetAvailableTransitions(task *ModerationTask, data map[string]interface{}) []ModerationStatus {
	var available []ModerationStatus
	for _, t := range sm.transitions {
		if ModerationStatus(task.CurrentStatus) == t.From {
			if t.Condition == nil || t.Condition(task, data) {
				available = append(available, t.To)
			}
		}
	}
	return available
}

func (sm *StateMachine) IsTerminal(status ModerationStatus) bool {
	terminalStatuses := []ModerationStatus{
		StatusPublished,
		StatusBanned,
		StatusAppealRejected,
	}
	for _, ts := range terminalStatuses {
		if status == ts {
			return true
		}
	}
	return false
}

func CalculateSLADeadline(cfg *config.Config) time.Time {
	return time.Now().Add(time.Duration(cfg.Moderation.SLAHours) * time.Hour)
}
