package core

import (
	"context"
	"time"
)

// DebateStatusView 是 GET /api/debates/{id}/status 的 core DTO（platform 直接返回）。
type DebateStatusView struct {
	ConversationID   string         `json:"conversation_id"`
	DebateID         string         `json:"debate_id"`
	Submissions      int            `json:"submissions"`
	Round            int            `json:"round"`
	PerPerson        map[string]int `json:"per_person"`
	Stalled          bool           `json:"stalled"`
	TimeboxExpired   bool           `json:"timebox_expired"`
	OpenedAt         string         `json:"opened_at,omitempty"`
	NextDeadline     string         `json:"next_deadline,omitempty"`
	RemainingSeconds int64          `json:"remaining_seconds"`
	LastSubmissionAt string         `json:"last_submission_at,omitempty"`
	TimeboxSeconds   int64          `json:"timebox_seconds"`
}

func parseSubmissionTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// DebateStatus 返回提交数/轮次/每方计数/stall/时间盒状态；超时写一次 debate.timeout event+audit。
func (s *DebateService) DebateStatus(ctx context.Context, conversationID, debateID string) (DebateStatusView, error) {
	subs, err := s.loadSubmissions(ctx, conversationID, debateID)
	if err != nil {
		return DebateStatusView{}, err
	}
	view := DebateStatusView{
		ConversationID: conversationID,
		DebateID:       debateID,
		Submissions:    len(subs),
		PerPerson:      map[string]int{},
		TimeboxSeconds: int64(s.timebox / time.Second),
	}
	var openedAt, lastAt time.Time
	for _, sub := range subs {
		view.PerPerson[sub.ActorID]++
		if sub.Round > view.Round {
			view.Round = sub.Round
		}
		if t, ok := parseSubmissionTime(sub.At); ok {
			if openedAt.IsZero() || t.Before(openedAt) {
				openedAt = t
			}
			if t.After(lastAt) {
				lastAt = t
			}
		}
	}
	view.Stalled = DetectStall(subs)
	if !lastAt.IsZero() {
		view.LastSubmissionAt = lastAt.Format(time.RFC3339Nano)
	}
	now := s.now()
	if openedAt.IsZero() {
		openedAt = now
	}
	view.OpenedAt = openedAt.Format(time.RFC3339Nano)
	deadline := openedAt.Add(s.timebox)
	view.NextDeadline = deadline.Format(time.RFC3339Nano)
	if now.After(deadline) {
		view.TimeboxExpired = true
	} else {
		view.RemainingSeconds = int64(deadline.Sub(now) / time.Second)
	}
	if view.TimeboxExpired {
		key := conversationID + "|" + debateID
		s.timeoutMu.Lock()
		already := s.timeoutMarked[key]
		if !already {
			s.timeoutMarked[key] = true
		}
		s.timeoutMu.Unlock()
		if !already {
			s.emit(ctx, "debate.timeout", "debate", debateID, "system", "core", map[string]any{
				"conversation_id": conversationID, "opened_at": view.OpenedAt,
				"deadline": view.NextDeadline, "submissions": view.Submissions,
			})
			s.audit(ctx, "system", "core", "debate.timeout", "debate", debateID, "ok", map[string]any{
				"conversation_id": conversationID, "deadline": view.NextDeadline,
			})
		}
	}
	return view, nil
}
