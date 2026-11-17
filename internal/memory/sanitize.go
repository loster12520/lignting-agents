package memory

import (
	"regexp"
	"strings"
)

// 敏感内容分两级：levelSecret 写入前必须清除（密钥/令牌/私钥/口令）；
// levelPII 属于个人身份信息，默认脱敏但允许配置放开。
const (
	levelPII    = 1
	levelSecret = 2
)

type sensitivePattern struct {
	name  string
	level int
	re    *regexp.Regexp
}

// 注意顺序：先匹配更长的结构（私钥块），再匹配具体 token，最后是赋值式与证件号。
var sensitivePatterns = []sensitivePattern{
	{"private_key", levelSecret, regexp.MustCompile("(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----")},
	{"private_key", levelSecret, regexp.MustCompile("-----BEGIN [A-Z ]*PRIVATE KEY-----")},
	{"api_key", levelSecret, regexp.MustCompile("\\bsk-[A-Za-z0-9_\\-]{16,}")},
	{"aws_key", levelSecret, regexp.MustCompile("\\bAKIA[0-9A-Z]{16}\\b")},
	{"github_token", levelSecret, regexp.MustCompile("\\bgh[pousr]_[A-Za-z0-9]{20,}")},
	{"bearer", levelSecret, regexp.MustCompile("(?i)\\bbearer\\s+[A-Za-z0-9\\-._~+/]{16,}=*")},
	{"credential", levelSecret, regexp.MustCompile("(?i)\\b(password|passwd|pwd|secret|token|api[_-]?key)\\s*[:=]\\s*\\S{4,}")},
	{"cn_id", levelPII, regexp.MustCompile("\\b\\d{17}[\\dXx]\\b")},
	{"cn_phone", levelPII, regexp.MustCompile("\\b1[3-9]\\d{9}\\b")},
}

// RedactResult 是脱敏结果。Cleaned 可直接落库/注入。
type RedactResult struct {
	Cleaned string
	Kinds   []string
	// SecretRemaining 表示清除后仍能匹配到密钥级模式（嵌套编码等），调用方应拒写。
	SecretRemaining bool
}

// Redact 把敏感片段替换为 [REDACTED:<kind>]，返回命中的类别（去重）。
func Redact(text string) RedactResult {
	out := text
	seen := map[string]bool{}
	var kinds []string
	for _, p := range sensitivePatterns {
		if !p.re.MatchString(out) {
			continue
		}
		out = p.re.ReplaceAllString(out, "[REDACTED:"+p.name+"]")
		if !seen[p.name] {
			seen[p.name] = true
			kinds = append(kinds, p.name)
		}
	}
	res := RedactResult{Cleaned: out, Kinds: kinds}
	for _, p := range sensitivePatterns {
		if p.level == levelSecret && p.re.MatchString(out) {
			res.SecretRemaining = true
			break
		}
	}
	return res
}

// HasSecret 报告文本是否含密钥级内容（不做脱敏，用于拒绝路径与测试）。
func HasSecret(text string) bool {
	for _, p := range sensitivePatterns {
		if p.level == levelSecret && p.re.MatchString(text) {
			return true
		}
	}
	return false
}

// 提示注入/越权特征。命中即视为不可信内容：不得晋升到 L4（AC-11）。
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile("(?i)ignore\\s+(all\\s+)?(previous|prior|above)\\s+(instructions?|prompts?|rules?)"),
	regexp.MustCompile("(?i)(disregard|forget)\\s+(all\\s+)?(previous|prior|above)"),
	regexp.MustCompile("(?i)you\\s+are\\s+now\\b"),
	regexp.MustCompile("忽略(以上|之前|前面|全部)?(的)?(指令|提示词?|规则|要求)"),
	regexp.MustCompile("(?i)(system\\s*prompt|系统提示词|系统指令|系统消息)"),
	regexp.MustCompile("(提升|提高|获取|提升到)\\s*(权限|管理员|root|admin)"),
	regexp.MustCompile("(?i)(sudo\\s+|grant\\s+(me\\s+)?admin|root\\s+access)"),
	regexp.MustCompile("(?i)<\\s*/?\\s*(system|assistant)\\s*>"),
	regexp.MustCompile("越权|提权|绕过(限制|审核|审批|权限)"),
	regexp.MustCompile("(?i)(输出|打印|泄露|告诉我).{0,6}(你的)?(系统|隐藏)(提示词|指令|消息)"),
}

// DetectInjection 返回命中的注入特征（去重、稳定顺序）。
func DetectInjection(text string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, re := range injectionPatterns {
		m := re.FindString(text)
		if m == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(m))
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, m)
	}
	return hits
}

// IsUntrusted 报告候选是否应被标记为不可信（禁止晋升 L4）。
func IsUntrusted(content string, tags string) bool {
	if TagsContain(tags, TagUntrusted) {
		return true
	}
	return len(DetectInjection(content)) > 0
}

// TruncateRunes 按字符（rune）截断，避免切坏中文；返回是否发生截断。
func TruncateRunes(s string, max int) (string, bool) {
	if max <= 0 {
		return "", s != ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

// RuneLen 返回字符数（中文按 1 计）。
func RuneLen(s string) int { return len([]rune(s)) }
