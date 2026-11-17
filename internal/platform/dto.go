package platform

import (
	"context"
	"strconv"
	"strings"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

type ownerDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HasPassword bool   `json:"has_password"`
	CreatedAt   string `json:"created_at"`
}

func toOwnerDTO(o *store.Owner) ownerDTO {
	if o == nil {
		return ownerDTO{}
	}
	return ownerDTO{ID: o.ID, Name: o.Name, HasPassword: o.HasPassword(), CreatedAt: o.CreatedAt}
}

// conversationDTO 是会话列表/详情的前端投影（含未读、最新消息、成员数）。
type conversationDTO struct {
	domain.Conversation
	Members     []domain.ConversationMember `json:"members"`
	MemberCount int                         `json:"member_count"`
	LastMessage *domain.Message             `json:"last_message"`
	LastSeq     int64                       `json:"last_seq"`
	ReadSeq     int64                       `json:"read_seq"`
	UnreadCount int64                       `json:"unread_count"`
}

func (s *Server) conversationDTO(ctx context.Context, c domain.Conversation) (conversationDTO, error) {
	members, err := s.store.ListConversationMembers(ctx, c.ID)
	if err != nil {
		return conversationDTO{}, err
	}
	last, err := s.store.LastMessage(ctx, c.ID)
	if err != nil {
		return conversationDTO{}, err
	}
	lastSeq, err := s.store.MaxMessageSeq(ctx, c.ID)
	if err != nil {
		return conversationDTO{}, err
	}
	readSeq, err := s.store.GetReadSeq(ctx, c.ID)
	if err != nil {
		return conversationDTO{}, err
	}
	unread, err := s.store.CountUnread(ctx, c.ID, readSeq)
	if err != nil {
		return conversationDTO{}, err
	}
	return conversationDTO{
		Conversation: c,
		Members:      members,
		MemberCount:  len(members),
		LastMessage:  last,
		LastSeq:      lastSeq,
		ReadSeq:      readSeq,
		UnreadCount:  unread,
	}, nil
}

func (s *Server) conversationDTOs(ctx context.Context, convs []domain.Conversation) ([]conversationDTO, error) {
	out := make([]conversationDTO, 0, len(convs))
	for _, c := range convs {
		dto, err := s.conversationDTO(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// settingsDTO 是 /api/settings 与 bootstrap 的设置投影（永不含密钥）。
type settingsDTO struct {
	OwnerName        string  `json:"owner_name"`
	Locale           string  `json:"locale"`
	DailyBudgetCNY   float64 `json:"daily_budget_cny"`
	MonthlyBudgetCNY float64 `json:"monthly_budget_cny"`
	Model            string  `json:"model"`
	ModelMode        string  `json:"model_mode"`
	HasAPIKey        bool    `json:"has_api_key"`
	SearchIndexState string  `json:"search_index_state"`
	Version          string  `json:"version"`
	SchemaVersion    int     `json:"schema_version"`
}

func settingsFloat(settings store.Settings, key string, fallback float64) float64 {
	v, ok := settings[key]
	if !ok {
		return fallback
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return fallback
	}
	return f
}

func formatBudget(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func (s *Server) settingsData(ctx context.Context, owner *store.Owner) (settingsDTO, error) {
	settings, err := s.store.SettingsWithPrefix(ctx, "")
	if err != nil {
		return settingsDTO{}, err
	}
	name := settings["owner_name"]
	if name == "" && owner != nil {
		name = owner.Name
	}
	locale := settings["locale"]
	if locale == "" {
		locale = "zh-CN"
	}
	model := s.cfg.Model
	if v := settings["model"]; v != "" {
		model = v
	}
	return settingsDTO{
		OwnerName:        name,
		Locale:           locale,
		DailyBudgetCNY:   settingsFloat(settings, "daily_budget_cny", s.cfg.DailyBudgetCNY),
		MonthlyBudgetCNY: settingsFloat(settings, "monthly_budget_cny", s.cfg.MonthlyBudgetCNY),
		Model:            model,
		ModelMode:        s.cfg.ModelMode,
		HasAPIKey:        s.hasAPIKey(),
		SearchIndexState: s.indexStateValue(),
		Version:          config.Version,
		SchemaVersion:    store.SchemaVersion,
	}, nil
}

// messageListDTO 是消息分页响应。
type messageListDTO struct {
	Items  []domain.Message `json:"items"`
	Total  int64            `json:"total"`
	Next   string           `json:"next"`
	MaxSeq int64            `json:"max_seq"`
}
