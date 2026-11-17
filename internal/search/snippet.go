package search

import (
	"html"
	"strings"
	"unicode/utf8"
)

// Snippet 生成安全片段：正文/标题中命中词用 <em> 包裹，其余字符全部 HTML 转义。
// 返回的 HTML 只可能包含 <em>/</em> 两个标签，前端可安全渲染。
func Snippet(body, title string, terms []string, width int) string {
	src := body
	if strings.TrimSpace(src) == "" {
		src = title
	}
	if src == "" {
		return ""
	}
	if width <= 0 {
		width = 160
	}
	runes := []rune(src)
	lower := []rune(strings.ToLower(src))
	if len(lower) != len(runes) {
		lower = runes // 长度变化（罕见）时退化为精确大小写匹配
	}
	clean := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			clean = append(clean, t)
		}
	}
	best := -1
	for _, t := range clean {
		tr := []rune(t)
		if idx := indexRunes(lower, tr); idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	start := 0
	if best > width/3 {
		start = best - width/3
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
	}
	if start > len(runes) {
		start = len(runes)
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	for i := start; i < end; {
		matchLen := 0
		for _, t := range clean {
			tr := []rune(t)
			if i+len(tr) > end {
				continue
			}
			if equalRunesAt(lower, tr, i) && len(tr) > matchLen {
				matchLen = len(tr)
			}
		}
		if matchLen > 0 {
			b.WriteString("<em>")
			b.WriteString(html.EscapeString(string(runes[i : i+matchLen])))
			b.WriteString("</em>")
			i += matchLen
			continue
		}
		b.WriteString(html.EscapeString(string(runes[i])))
		i++
	}
	if end < len(runes) {
		b.WriteString("…")
	}
	if b.Len() == 0 {
		return html.EscapeString(truncateRunes(src, width))
	}
	return b.String()
}

func indexRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if equalRunesAt(haystack, needle, i) {
			return i
		}
	}
	return -1
}

func equalRunesAt(haystack, needle []rune, at int) bool {
	if at < 0 || at+len(needle) > len(haystack) {
		return false
	}
	for i, r := range needle {
		if haystack[at+i] != r {
			return false
		}
	}
	return true
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
