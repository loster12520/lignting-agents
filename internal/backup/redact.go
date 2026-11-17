package backup

import (
	"encoding/json"
	"regexp"
	"strings"
)

var sensitiveKeyRe = regexp.MustCompile(`(?i)^(api[_-]?key|key|token|password|passwd|secret|authorization|cookie|credential|access[_-]?key|private[_-]?key|client[_-]?secret)$`)

var secretValueRes = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{12,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret)\s*[:=]\s*[A-Za-z0-9._-]{12,}`),
}

// redactText removes known secret values and common credential-shaped strings.
func redactText(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[REDACTED]")
		}
	}
	for _, re := range secretValueRes {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// redactAny recursively redacts sensitive keys and credential-shaped values.
func redactAny(v any, secrets []string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if sensitiveKeyRe.MatchString(strings.TrimSpace(k)) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactAny(val, secrets)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactAny(val, secrets)
		}
		return out
	case string:
		return redactText(x, secrets)
	default:
		return v
	}
}

// redactJSONText redacts string fields of a JSON object/array when possible.
func redactJSONText(s string, secrets []string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return s
	}
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return redactText(s, secrets)
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return redactText(s, secrets)
	}
	out, err := json.Marshal(redactAny(v, secrets))
	if err != nil {
		return redactText(s, secrets)
	}
	return string(out)
}
