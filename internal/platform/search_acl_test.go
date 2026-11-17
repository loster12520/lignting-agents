package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

type agentToken struct {
	agent domain.Agent
	token string
}

func newAgentWithToken(t *testing.T, st *store.Store, name, kind, deptID string) agentToken {
	t.Helper()
	plain, hash, err := store.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	agent, err := st.CreateAgent(context.Background(), domain.Agent{
		Name: name, Kind: kind, DepartmentID: deptID, TokenHash: hash,
	})
	if err != nil {
		t.Fatalf("agent %s: %v", name, err)
	}
	return agentToken{agent: agent, token: plain}
}

func (e *env) searchAs(t *testing.T, token, q string) map[string]any {
	t.Helper()
	status, out := e.do("GET", "/api/search?q="+q, nil, func(r *http.Request) {
		r.Header.Set("X-Lignting-Agent-Token", token)
	})
	return e.mustOK(status, out)
}

func TestSearchACLByAgentToken(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	deptA, err := e.st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	if err != nil {
		t.Fatalf("deptA: %v", err)
	}
	deptB, err := e.st.CreateDepartment(ctx, domain.Department{Name: "测试部", Kind: "qa"})
	if err != nil {
		t.Fatalf("deptB: %v", err)
	}
	empA := newAgentWithToken(t, e.st, "研发员工", domain.AgentKindEmployee, deptA.ID)
	empB := newAgentWithToken(t, e.st, "测试员工", domain.AgentKindEmployee, deptB.ID)
	assistantToken := newAgentWithToken(t, e.st, "助理2", domain.AgentKindAssistant, "")
	_ = assistantToken // 助理视野用 setup 助理的身份验证；这里也用独立助理 token

	mkConv := func(title, deptID, visibility string, agents ...domain.Agent) domain.Conversation {
		members := make([]domain.ConversationMember, 0, len(agents)+1)
		for _, a := range agents {
			members = append(members, domain.ConversationMember{PrincipalType: domain.SenderAgent, PrincipalID: a.ID, Role: "member"})
		}
		c, err := e.st.CreateConversation(ctx, domain.Conversation{
			Type: domain.ConvDepartment, Title: title, DepartmentID: deptID,
			Visibility: visibility, CreatedBy: "owner",
		}, members, store.Actor{Type: domain.SenderSystem, ID: "system"})
		if err != nil {
			t.Fatalf("conv %s: %v", title, err)
		}
		return c
	}
	post := func(conv domain.Conversation, sender domain.Agent, content string) {
		if _, _, _, err := e.st.InsertMessage(ctx, store.NewMessage{
			ConversationID: conv.ID, SenderType: domain.SenderAgent, SenderID: sender.ID,
			SenderName: sender.Name, Type: domain.MsgText, Content: content,
		}); err != nil {
			t.Fatalf("message: %v", err)
		}
	}
	convA := mkConv("研发群", deptA.ID, domain.VisibilityDepartment, empA.agent)
	convB := mkConv("测试群", deptB.ID, domain.VisibilityDepartment, empB.agent)
	humanConv := mkConv("人类专属", deptA.ID, domain.VisibilityHumanOnly, empA.agent)
	privateConv := mkConv("私聊", "", domain.VisibilityPrivate, empB.agent, assistantToken.agent)
	post(convA, empA.agent, "研发部 限流方案")
	post(convB, empB.agent, "测试部 绝密部署")
	post(humanConv, empA.agent, "人类专属机密")
	post(privateConv, empB.agent, "私聊秘密")

	// owner（人类）全部可见
	for _, q := range []string{"限流", "绝密", "人类专属", "私聊"} {
		if total := e.search(t, q)["total"].(float64); total != 1 {
			t.Fatalf("owner search %q total = %v, want 1", q, total)
		}
	}
	// 员工 A：本部门 + 自己是成员的会话；看不到他部门/人类专属/他人私聊
	if total := e.searchAs(t, empA.token, "限流")["total"].(float64); total != 1 {
		t.Fatalf("empA own dept total = %v, want 1", total)
	}
	for _, q := range []string{"绝密", "人类专属", "私聊"} {
		if total := e.searchAs(t, empA.token, q)["total"].(float64); total != 0 {
			t.Fatalf("empA leak %q total = %v, want 0", q, total)
		}
	}
	// 员工 B：只看得到自己部门
	if total := e.searchAs(t, empB.token, "绝密")["total"].(float64); total != 1 {
		t.Fatalf("empB own dept total = %v, want 1", total)
	}
	if total := e.searchAs(t, empB.token, "限流")["total"].(float64); total != 0 {
		t.Fatalf("empB cross dept leak total = %v", total)
	}
	// 助理：非私有群聊全可见；人类专属与私有仍不可见
	if total := e.searchAs(t, assistantToken.token, "限流")["total"].(float64); total != 1 {
		t.Fatalf("assistant 限流 total = %v", total)
	}
	if total := e.searchAs(t, assistantToken.token, "绝密")["total"].(float64); total != 1 {
		t.Fatalf("assistant 绝密 total = %v", total)
	}
	for _, q := range []string{"人类专属", "私聊"} {
		if total := e.searchAs(t, assistantToken.token, q)["total"].(float64); total != 0 {
			t.Fatalf("assistant leak %q total = %v, want 0", q, total)
		}
	}
	// 无效 Agent Token → 401
	if status, _ := e.do("GET", "/api/search?q=限流", nil, func(r *http.Request) {
		r.Header.Set("X-Lignting-Agent-Token", "lgt_invalid")
	}); status != http.StatusUnauthorized {
		t.Fatalf("invalid agent token status = %d, want 401", status)
	}
}

func TestPasswordPostAlias(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	status, out := e.doCSRF("POST", "/api/settings/password", map[string]any{"new_password": "newpass123"})
	if status != http.StatusOK {
		t.Fatalf("POST password = %d %v", status, out)
	}
	if e.mustOK(status, out)["has_password"] != true {
		t.Fatalf("password result = %v", out)
	}
}
