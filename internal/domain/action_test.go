package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseActionsSingle(t *testing.T) {
	text := "我先建个任务。\n\n```lignting-action\n{\"action\":\"create_task\",\"args\":{\"title\":\"导出 CSV\",\"department_id\":\"dept_1\",\"perspective\":\"接口\",\"acceptance\":[\"可下载\"]}}\n```\n\n请确认。"
	clean, actions, errs := ParseActions(text)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(actions) != 1 || actions[0].Action != "create_task" {
		t.Fatalf("actions = %#v", actions)
	}
	var args map[string]any
	if err := json.Unmarshal(actions[0].Args, &args); err != nil {
		t.Fatal(err)
	}
	if args["title"] != "导出 CSV" {
		t.Fatalf("args = %#v", args)
	}
	if strings.Contains(clean, "lignting-action") || strings.Contains(clean, "create_task") {
		t.Fatalf("clean text still contains action: %q", clean)
	}
	if !strings.Contains(clean, "我先建个任务") || !strings.Contains(clean, "请确认") {
		t.Fatalf("clean text lost content: %q", clean)
	}
}

func TestParseActionsMultiple(t *testing.T) {
	text := "```lignting-action\n{\"action\":\"save_intent\",\"args\":{\"content\":\"x\"}}\n```\n中间\n```lignting-action\n{\"action\":\"create_decision\",\"args\":{\"title\":\"t\",\"decision\":\"d\",\"rationale\":\"r\"}}\n```\n结尾"
	clean, actions, errs := ParseActions(text)
	if len(errs) != 0 || len(actions) != 2 {
		t.Fatalf("errs=%v actions=%d", errs, len(actions))
	}
	if !strings.Contains(clean, "中间") || !strings.Contains(clean, "结尾") {
		t.Fatalf("clean=%q", clean)
	}
}

func TestParseActionsInvalidJSON(t *testing.T) {
	text := "前\n```lignting-action\n{not json}\n```\n后"
	clean, actions, errs := ParseActions(text)
	if len(actions) != 0 || len(errs) != 1 {
		t.Fatalf("actions=%d errs=%v", len(actions), errs)
	}
	if strings.Contains(clean, "not json") {
		t.Fatalf("invalid block should be removed from clean text: %q", clean)
	}
}

func TestParseActionsUnknownAction(t *testing.T) {
	text := "```lignting-action\n{\"action\":\"delete_everything\",\"args\":{}}\n```"
	_, actions, errs := ParseActions(text)
	if len(actions) != 0 || len(errs) != 1 {
		t.Fatalf("actions=%d errs=%v", len(actions), errs)
	}
}

func TestParseActionsNoBlocks(t *testing.T) {
	text := "普通回复，没有动作。"
	clean, actions, errs := ParseActions(text)
	if clean != text || len(actions) != 0 || len(errs) != 0 {
		t.Fatalf("clean=%q actions=%v errs=%v", clean, actions, errs)
	}
}

func TestActionValidate(t *testing.T) {
	ok := Action{Action: "create_task", Args: []byte(`{"title":"t","department_id":"d","perspective":"p","acceptance":["a"]}`)}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid action rejected: %v", err)
	}
	missing := Action{Action: "create_task", Args: []byte(`{"title":"t"}`)}
	if err := missing.Validate(); err == nil {
		t.Fatal("missing required args should fail")
	}
	bad := Action{Action: "create_task", Args: []byte(`[]`)}
	if err := bad.Validate(); err == nil {
		t.Fatal("non-object args should fail")
	}
	unknown := Action{Action: "nope"}
	if err := unknown.Validate(); err == nil {
		t.Fatal("unknown action should fail")
	}
}

