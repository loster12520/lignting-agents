package domain

import "testing"

func TestCanTransitionHappyPath(t *testing.T) {
	path := []string{"proposed", "ready", "discussing", "in_progress", "in_review", "approved", "done"}
	for i := 0; i < len(path)-1; i++ {
		if !CanTransition(path[i], path[i+1]) {
			t.Fatalf("expected %s -> %s allowed", path[i], path[i+1])
		}
	}
}

func TestCanTransitionRejectsIllegal(t *testing.T) {
	cases := [][2]string{
		{"proposed", "done"},
		{"done", "in_progress"},
		{"canceled", "ready"},
		{"approved", "in_progress"},
		{"in_review", "discussing"},
	}
	for _, c := range cases {
		if CanTransition(c[0], c[1]) {
			t.Fatalf("expected %s -> %s rejected", c[0], c[1])
		}
	}
}

func TestTerminalStatuses(t *testing.T) {
	for _, s := range []string{"done", "canceled", "failed"} {
		if !IsTerminalTaskStatus(s) {
			t.Fatalf("%s should be terminal", s)
		}
	}
	for _, s := range []string{"proposed", "ready", "discussing", "in_progress", "blocked", "in_review", "approved"} {
		if IsTerminalTaskStatus(s) {
			t.Fatalf("%s should not be terminal", s)
		}
	}
}

func TestAllowedTransitionsCopy(t *testing.T) {
	got := AllowedTransitions("proposed")
	got[0] = "corrupted"
	if TaskTransitions["proposed"][0] == "corrupted" {
		t.Fatal("AllowedTransitions must return a copy")
	}
}
