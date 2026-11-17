package memory

import (
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func coreAgent(id, kind, dept string) domain.Agent {
	return domain.Agent{ID: id, Kind: kind, DepartmentID: dept}
}

func TestInjectAdapterReturnsSummariesOnly(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	sup := supervisor("sup_1", "dept_a")
	c := baseCandidate("t1")
	c.Content = "后端能力：Go + SQLite，可独立完成模块设计与实现；注意与前端联调接口"
	c.Summary = "后端能力：Go + SQLite"
	m := mustActive(t, svc, c, sup)

	rows, err := svc.Inject(ctx, coreAgent("sup_1", ActorSupervisor, "dept_a"), "conv_1", "task_1", 10)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != m.ID {
		t.Fatalf("应注入 1 条: %+v", rows)
	}
	if rows[0].Content != m.Summary {
		t.Fatalf("core 只应拿到摘要，得到 %q", rows[0].Content)
	}
	if !TagsContain(rows[0].Tags, "injected") {
		t.Fatalf("应标记 injected: %s", rows[0].Tags)
	}
	if !strings.Contains(rows[0].Tags, "src:memory://") {
		t.Fatalf("应带来源标签: %s", rows[0].Tags)
	}
}

func TestInjectAdapterRespectsLimitAndPermissions(t *testing.T) {
	svc, _, _, _ := newTestService(t, func(c *Config) { c.InjectAbsCapTokens = 4000 })
	sup := supervisor("sup_1", "dept_a")
	for i := 0; i < 3; i++ {
		c := baseCandidate("")
		c.EntityKey = "k" + string(rune('a'+i))
		c.Content = "能力条目 " + string(rune('A'+i))
		mustActive(t, svc, c, sup)
	}
	// human-only 与删除项不得出现。
	ho := Candidate{
		Scope: domain.MemoryScopeDepartment, DepartmentID: "dept_a", AgentID: "sup_1",
		EntityType: EntityPreference, EntityKey: "secret-pref",
		Content: "人类私密偏好", Confidence: ConfidenceHigh,
		Evidence:   []Evidence{{Kind: EvidenceHuman, Ref: "user_1"}},
		Visibility: domain.MemoryHumanOnly, CreatedBy: "user_1",
		Reusable: true, DecisionRelevant: true,
	}
	if _, err := svc.CreateDirect(ctx, ho, human("user_1")); err != nil {
		t.Fatalf("建 human-only: %v", err)
	}

	rows, err := svc.Inject(ctx, coreAgent("sup_1", ActorSupervisor, "dept_a"), "conv", "task", 2)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("limit 应生效: %d", len(rows))
	}

	all, _ := svc.Inject(ctx, coreAgent("sup_1", ActorSupervisor, "dept_a"), "conv", "task", 0)
	for _, m := range all {
		if strings.Contains(m.Content, "人类私密偏好") {
			t.Fatal("human-only 不得注入")
		}
	}
}

func TestInjectAdapterAfterDelete(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	sup := supervisor("sup_1", "dept_a")
	m := mustActive(t, svc, baseCandidate("t1"), sup)
	rows, _ := svc.Inject(ctx, coreAgent("sup_1", ActorSupervisor, "dept_a"), "c", "t", 0)
	if len(rows) != 1 {
		t.Fatalf("删除前应注入: %d", len(rows))
	}
	if err := svc.Delete(ctx, m.ID, "测试", human("user_1")); err != nil {
		t.Fatalf("删除: %v", err)
	}
	rows, _ = svc.Inject(ctx, coreAgent("sup_1", ActorSupervisor, "dept_a"), "c", "t", 0)
	if len(rows) != 0 {
		t.Fatalf("删除后不得注入: %+v", rows)
	}
}

func TestManualLayers(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	agent := coreAgent("sup_1", ActorSupervisor, "dept_a")
	key := ManualKey{Scope: domain.MemoryScopeDepartment, DepartmentID: "dept_a"}

	layers, version, found, err := svc.ManualLayers(ctx, agent, "ROLE_CARD")
	if err != nil || found || version != 0 {
		t.Fatalf("无已批准手册时 found=false: %v %v %v", found, version, err)
	}
	if !strings.Contains(layers.Render(), "ROLE_CARD") {
		t.Fatal("无手册时仍应返回角色卡")
	}

	v1, _ := svc.ProposeManual(ctx, key, manualV1, nil, human("user_1"))
	if _, err := svc.ApproveManual(ctx, v1.ID, human("user_1")); err != nil {
		t.Fatalf("批准: %v", err)
	}
	layers, version, found, err = svc.ManualLayers(ctx, agent, "ROLE_CARD")
	if err != nil || !found || version != 1 {
		t.Fatalf("应找到已批准手册 v1: %v %v %v", found, version, err)
	}
	rendered := layers.Render()
	if !strings.Contains(rendered, "对外承诺必须升级") || !strings.Contains(rendered, "ROLE_CARD") {
		t.Fatalf("手册与角色卡都应渲染: %s", rendered)
	}
}
