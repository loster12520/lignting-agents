package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// R6 辩论硬边界（requirements FR-DEB-03 / FR-TASK-08）。
const (
	MaxStancePerPerson = 5
	MaxStanceTotal     = 15
	MaxDebateRound     = 2
	DebateReopenLimit  = 1
	// DefaultDebateTimebox 是异步时间盒（可配）。
	DefaultDebateTimebox = 24 * time.Hour
)

// 升级阶梯 L0–L4（FR-ESC-01）。
const (
	EscalationL0 = "L0" // 自检/测试
	EscalationL1 = "L1" // 部门内
	EscalationL2 = "L2" // 跨部门（助理主持）
	EscalationL3 = "L3" // 人类（预算/范围/不可逆/质量豁免）
	EscalationL4 = "L4" // 死锁/重大风险
)

// Submission 是一条辩论/讨论提交（从消息 meta 投影）。
type Submission struct {
	ActorID     string   `json:"actor_id"`
	Round       int      `json:"round"`
	Stance      string   `json:"stance"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	At          string   `json:"at,omitempty"`
}

// CheckStanceLimits 校验每人≤5、总≤15、轮次≤2；返回可读错误（nil 表示通过）。
func CheckStanceLimits(subs []Submission, actorID string, round int) error {
	if round < 1 || round > MaxDebateRound {
		return fmt.Errorf("轮次超出范围（1..%d）：%d", MaxDebateRound, round)
	}
	perPerson := 0
	for _, s := range subs {
		if s.ActorID == actorID {
			perPerson++
		}
	}
	if perPerson >= MaxStancePerPerson {
		return fmt.Errorf("每人最多 %d 条提交（当前 %d）", MaxStancePerPerson, perPerson)
	}
	if len(subs) >= MaxStanceTotal {
		return fmt.Errorf("总提交最多 %d 条（当前 %d）", MaxStanceTotal, len(subs))
	}
	return nil
}

// HasNewEvidence 判断本次提交是否带来此前未出现过的证据 ID。
func HasNewEvidence(prev []Submission, cur Submission) bool {
	seen := map[string]bool{}
	for _, s := range prev {
		for _, id := range s.EvidenceIDs {
			seen[id] = true
		}
	}
	for _, id := range cur.EvidenceIDs {
		if !seen[id] {
			return true
		}
	}
	return false
}

// DetectStall 判断是否停滞：连续两次提交 stance 相同且第二次无新证据。
func DetectStall(subs []Submission) bool {
	if len(subs) < 2 {
		return false
	}
	last := subs[len(subs)-1]
	if last.Stance == "" {
		return false
	}
	// 上一条立场相同，且本次没有引入此前未出现的证据 → 停滞。
	previous := subs[len(subs)-2]
	if previous.Stance != last.Stance {
		return false
	}
	return !HasNewEvidence(subs[:len(subs)-1], last)
}

// TimeboxExceeded 判断时间盒是否到期。
func TimeboxExceeded(openedAt, now time.Time, timebox time.Duration) bool {
	if timebox <= 0 {
		timebox = DefaultDebateTimebox
	}
	return now.After(openedAt.Add(timebox))
}

// ParseSubmissionMeta 从消息 meta 解析辩论提交；非辩论消息返回 (nil,false)。
func ParseSubmissionMeta(meta string) (Submission, bool) {
	var m struct {
		Kind        string   `json:"kind"`
		ActorID     string   `json:"actor_id"`
		Round       int      `json:"round"`
		Stance      string   `json:"stance"`
		EvidenceIDs []string `json:"evidence_ids"`
		At          string   `json:"at"`
	}
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return Submission{}, false
	}
	if m.Kind != "debate.stance" && m.Kind != "dept.discussion" {
		return Submission{}, false
	}
	return Submission{ActorID: m.ActorID, Round: m.Round, Stance: m.Stance, EvidenceIDs: m.EvidenceIDs, At: m.At}, true
}
