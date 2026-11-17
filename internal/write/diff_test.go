package write

import (
	"strings"
	"testing"
)

func TestDiffBasic(t *testing.T) {
	d := Diff("a.txt", "one\ntwo\nthree\n", "one\nTWO\nthree\n")
	if !strings.Contains(d, "--- a/a.txt") || !strings.Contains(d, "+++ b/a.txt") {
		t.Fatalf("missing headers: %q", d)
	}
	if !strings.Contains(d, "@@") {
		t.Fatalf("missing hunk: %q", d)
	}
	if !strings.Contains(d, "-two") || !strings.Contains(d, "+TWO") {
		t.Fatalf("missing change lines: %q", d)
	}
	if !strings.Contains(d, " one") {
		t.Fatalf("missing context: %q", d)
	}
}

func TestDiffNoChange(t *testing.T) {
	if d := Diff("a.txt", "same\n", "same\n"); d != "" {
		t.Fatalf("want empty diff, got %q", d)
	}
}

func TestDiffMultipleHunks(t *testing.T) {
	oldLines := make([]string, 40)
	newLines := make([]string, 40)
	for i := range oldLines {
		oldLines[i] = "line"
		newLines[i] = "line"
	}
	newLines[1] = "changed-one"
	newLines[35] = "changed-two"
	d := Diff("a.txt", strings.Join(oldLines, "\n")+"\n", strings.Join(newLines, "\n")+"\n")
	if got := strings.Count(d, "\n@@ "); got != 2 {
		t.Fatalf("hunks = %d, diff=%q", got, d)
	}
	if !strings.Contains(d, "+changed-one") || !strings.Contains(d, "+changed-two") {
		t.Fatalf("missing changes: %q", d)
	}
}

func TestDiffLargeFallback(t *testing.T) {
	oldText := strings.Repeat("old-line\n", 3000)
	newText := strings.Repeat("new-line\n", 3000)
	d := Diff("big.txt", oldText, newText)
	if !strings.Contains(d, "diff omitted") {
		t.Fatalf("want fallback, got %q", d[:min(80, len(d))])
	}
}
