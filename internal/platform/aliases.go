package platform

import (
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// 契约 §12.4：接受旧别名，写库前归一化到统一词表（不修改 internal/domain 常量）。

// normalizeConversationVisibility 归一化会话可见性（conversations/搜索投影共用）：
//
//	private | agent-private -> private
//	department | team      -> department
//	project                -> project
//	org | organization     -> org
//	human_only | human-only-> human-only
func normalizeConversationVisibility(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "_", "-")
	switch v {
	case "":
		return "", nil
	case "private", "agent-private":
		return domain.VisibilityPrivate, nil
	case "department", "team":
		return domain.VisibilityDepartment, nil
	case "project":
		return domain.VisibilityProject, nil
	case "org", "organization":
		return domain.VisibilityOrg, nil
	case "human-only":
		return domain.VisibilityHumanOnly, nil
	}
	return "", errInvalid("不支持的可见性：" + raw)
}

// normalizeMemoryVisibility 归一化记忆可见性：
//
//	agent-private | agent_private | private -> agent-private
//	team | department                      -> team
//	org                                    -> org
//	human_only | human-only                -> human-only
func normalizeMemoryVisibility(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "_", "-")
	switch v {
	case "":
		return "", nil
	case "agent-private", "private":
		return domain.MemoryAgentPrivate, nil
	case "team", "department":
		return domain.MemoryTeam, nil
	case "org":
		return domain.MemoryOrg, nil
	case "human-only":
		return domain.MemoryHumanOnly, nil
	}
	return "", errInvalid("不支持的记忆可见性：" + raw)
}

var memoryEntityAliases = map[string]string{
	"ability":      "capability",
	"abilities":    "capability",
	"capabilities": "capability",
}

var memoryEntityTypes = map[string]bool{
	"person": true, "employee": true, "department": true, "project": true,
	"task": true, "decision": true, "dispute": true, "capability": true, "preference": true,
}

// normalizeMemoryEntityType 归一化 entity_type：ability/abilities -> capability；未知 400。
func normalizeMemoryEntityType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", errInvalid("entity_type 必填")
	}
	if alias, ok := memoryEntityAliases[v]; ok {
		v = alias
	}
	if !memoryEntityTypes[v] {
		return "", errInvalid("未知实体类型：" + raw)
	}
	return v, nil
}
