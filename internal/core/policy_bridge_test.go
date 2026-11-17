package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

func bridgeFixture(t *testing.T) (*fakeStore, domain.Agent, domain.Agent, domain.Agent, domain.Conversation) {
	t.Helper()
	store := newFakeStore()
	empA := domain.Agent{ID: "agent_a", Name: "员工A", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	empB := domain.Agent{ID: "agent_b", Name: "员工B", Kind: domain.AgentKindEmployee, DepartmentID: "d2"}
	sup := domain.Agent{ID: "agent_sup", Name: "主管", Kind: domain.AgentKindSupervisor, DepartmentID: "d1"}
	for _, a := range []domain.Agent{empA, empB, sup} {
		store.seedAgent(a)
	}
	conv := domain.Conversation{ID: "conv_x", Type: domain.ConvProject, Title: "项目"}
	store.seedConversation(conv)
	return store, empA, empB, sup, conv
}

func activeBridge(fromType, fromID, toType, toID, scope string) domain.Bridge {
	return domain.Bridge{
		ID: "bridge_1", Scope: scope, FromType: fromType, FromID: fromID,
		ToType: toType, ToID: toID, Status: "active",
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	}
}

func TestBridgeRouteAndScope(t *testing.T) {
	store, empA, empB, _, conv := bridgeFixture(t)
	policy := NewPolicy(nil)
	policy.AttachBridges(store)
	ctx := context.Background()

	if err := policy.Route(ctx, empA, empB, conv); err == nil {
		t.Fatal("无桥跨部门员工直连应被拒绝")
	} else if !strings.Contains(err.Error(), "主管") && !strings.Contains(err.Error(), "临时桥") {
		t.Fatalf("拒绝提示缺少路径: %v", err)
	}

	// 错误 scope（lead_to_user）不能授权 agent→agent。
	setFakeBridge(store, activeBridge("agent", empA.ID, "agent", empB.ID, domain.BridgeLeadToUser))
	if err := policy.Route(ctx, empA, empB, conv); err == nil {
		t.Fatal("lead_to_user 桥不应授权 agent→agent")
	}
	// cross_dept 桥允许。
	setFakeBridge(store, activeBridge("agent", empA.ID, "agent", empB.ID, domain.BridgeCrossDept))
	if err := policy.Route(ctx, empA, empB, conv); err != nil {
		t.Fatalf("cross_dept 桥应允许: %v", err)
	}
	// 到期后拒绝。
	expired := activeBridge("agent", empA.ID, "agent", empB.ID, domain.BridgeCrossDept)
	expired.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	setFakeBridge(store, expired)
	if err := policy.Route(ctx, empA, empB, conv); err == nil {
		t.Fatal("过期桥不应允许直连")
	}
	// 关闭后拒绝。
	closed := activeBridge("agent", empA.ID, "agent", empB.ID, domain.BridgeAgentPair)
	closed.Status = "closed"
	setFakeBridge(store, closed)
	if err := policy.Route(ctx, empA, empB, conv); err == nil {
		t.Fatal("关闭桥不应允许直连")
	}
}

func TestBridgeParticipationAndNoApprovalBypass(t *testing.T) {
	store, _, _, sup, _ := bridgeFixture(t)
	policy := NewPolicy(nil)
	policy.AttachBridges(store)
	ctx := context.Background()
	dm := domain.Conversation{ID: "conv_dm", Type: domain.ConvDM, Title: "私聊"}
	members := []domain.ConversationMember{{ConversationID: dm.ID, PrincipalType: "user", PrincipalID: "owner"}}

	if err := policy.AuthorizeParticipation(ctx, sup, dm, members); err == nil {
		t.Fatal("无桥主管不应直接参与用户私聊")
	} else if !strings.Contains(err.Error(), "临时桥") {
		t.Fatalf("拒绝提示缺少开桥路径: %v", err)
	}
	setFakeBridge(store, activeBridge("agent", sup.ID, "user", "owner", domain.BridgeLeadToUser))
	if err := policy.AuthorizeParticipation(ctx, sup, dm, members); err != nil {
		t.Fatalf("lead_to_user 桥应允许主管进 DM: %v", err)
	}
	expired := activeBridge("agent", sup.ID, "user", "owner", domain.BridgeLeadToUser)
	expired.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	setFakeBridge(store, expired)
	if err := policy.AuthorizeParticipation(ctx, sup, dm, members); err == nil {
		t.Fatal("过期桥不应允许主管进 DM")
	}

	// 桥不能绕过审批/动作权限：员工 create_task 仍被拒。
	setFakeBridge(store, activeBridge("agent", sup.ID, "user", "owner", domain.BridgeAgentPair))
	employee := domain.Agent{ID: "agent_a", Name: "员工A", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	action := marshalAction(t, "create_task", map[string]any{
		"title": "x", "department_id": "d1", "perspective": "接口", "acceptance": []string{"a"},
	})
	if err := policy.AuthorizeAction(ctx, ActionContext{Agent: employee, Task: nil}, action); err == nil {
		t.Fatal("临时桥不应绕过动作/审批权限")
	}
}

// 平台桥 API 不强制方向/层级：反向登记与部门级登记也应生效。
func TestBridgeDirectionAndDepartmentRepresentation(t *testing.T) {
	store, empA, empB, sup, conv := bridgeFixture(t)
	policy := NewPolicy(nil)
	policy.AttachBridges(store)
	ctx := context.Background()

	setFakeBridge(store, activeBridge("agent", empB.ID, "agent", empA.ID, domain.BridgeCrossDept))
	if err := policy.Route(ctx, empA, empB, conv); err != nil {
		t.Fatalf("反向 agent 对桥应允许: %v", err)
	}

	deptStore, empA2, empB2, _, conv2 := bridgeFixture(t)
	deptPolicy := NewPolicy(nil)
	deptPolicy.AttachBridges(deptStore)
	setFakeBridge(deptStore, activeBridge("department", "d1", "department", "d2", domain.BridgeCrossDept))
	if err := deptPolicy.Route(ctx, empA2, empB2, conv2); err != nil {
		t.Fatalf("部门级 cross_dept 桥应允许: %v", err)
	}

	dm := domain.Conversation{ID: "conv_dm2", Type: domain.ConvDM, Title: "私聊"}
	members := []domain.ConversationMember{{ConversationID: dm.ID, PrincipalType: "user", PrincipalID: "owner"}}
	setFakeBridge(store, activeBridge("user", "owner", "agent", sup.ID, domain.BridgeLeadToUser))
	if err := policy.AuthorizeParticipation(ctx, sup, dm, members); err != nil {
		t.Fatalf("反向 lead_to_user 桥应允许主管进 DM: %v", err)
	}
}

func TestRuntimeAutoAttachBridges(t *testing.T) {
	store, empA, empB, _, conv := bridgeFixture(t)
	rt, err := NewRuntime(Options{Store: store, Provider: deepseek.NewMock()})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.CanAgentMessage(ctx, empA, empB, conv); err == nil {
		t.Fatal("无桥应拒绝")
	}
	setFakeBridge(store, activeBridge("agent", empA.ID, "agent", empB.ID, domain.BridgeAgentPair))
	if err := rt.CanAgentMessage(ctx, empA, empB, conv); err != nil {
		t.Fatalf("NewRuntime 应自动注入 store bridge: %v", err)
	}
}
