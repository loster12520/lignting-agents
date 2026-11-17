package backup

import (
	"strings"
	"testing"
)

func TestRedactAnyAndJSONText(t *testing.T) {
	secrets := []string{"sk-live-SECRET"}
	got := redactAny(map[string]any{
		"api_key": "sk-live-SECRET",
		"note":    "prefix sk-live-SECRET suffix",
		"nested": []any{map[string]any{"password": "p", "name": "ok"}},
	}, secrets).(map[string]any)
	if got["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key not redacted: %v", got["api_key"])
	}
	if strings.Contains(got["note"].(string), "sk-live-SECRET") {
		t.Fatalf("known secret leaked: %v", got["note"])
	}
	nested := got["nested"].([]any)[0].(map[string]any)
	if nested["password"] != "[REDACTED]" {
		t.Fatalf("nested password not redacted: %v", nested)
	}

	out := redactJSONText(`{"api_key":"sk-live-SECRET","note":"hello"}`, secrets)
	if strings.Contains(out, "sk-live-SECRET") || !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("json redaction failed: %s", out)
	}
}

func TestValidArchivePath(t *testing.T) {
	for _, ok := range []string{"lignting.db", "artifacts/a.md", "config.json"} {
		if !validArchivePath(ok) {
			t.Fatalf("expected valid: %s", ok)
		}
	}
	for _, bad := range []string{"", "../x", "/abs", "C:/x", "a/../../b", "a\\b"} {
		if validArchivePath(bad) {
			t.Fatalf("expected invalid: %q", bad)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent(`a"b`); got != `"a""b"` {
		t.Fatalf("quoteIdent = %s", got)
	}
}
