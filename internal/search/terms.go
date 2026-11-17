package search

import (
	"sort"
	"strings"
	"unicode"
)

// Term 是索引词及其权重。权重用于相关度排序，不影响是否命中。
type Term struct {
	Text   string
	Weight float64
}

// QueryTerm 是查询词。SingleHan 表示单个汉字：不入 bigram 索引，检索时回退 LIKE。
type QueryTerm struct {
	Text      string
	SingleHan bool
}

// 词权重：完整英文词/代码标识符 1.0；子词 0.6；单字中文 0.5。
const (
	weightWord      = 1.0
	weightSubword   = 0.6
	weightSingleHan = 0.5
)

// maxTermsPerDoc 限制单文档索引词数量，避免超长消息放大索引。
const maxTermsPerDoc = 5000

// normalizeRunes 做宽度/空白/零宽规范化，保留大小写（camelCase 需要原始大小写）。
func normalizeRunes(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == 0x00A0 || r == 0x3000 || unicode.IsSpace(r):
			out = append(out, ' ')
		case r >= 0xFF01 && r <= 0xFF5E: // 全角 ASCII -> 半角
			out = append(out, r-0xFEE0)
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF:
			continue // 零宽字符
		default:
			out = append(out, r)
		}
	}
	return out
}

// Normalize 返回检索/展示用的规范化小写文本。
func Normalize(s string) string {
	return strings.ToLower(string(normalizeRunes(s)))
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$'
}

// splitCamel 按 camelCase / 数字边界拆分标识符（保留原始大小写输入）。
func splitCamel(w string) []string {
	runes := []rune(w)
	if len(runes) == 0 {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		if !unicode.IsUpper(cur) {
			continue
		}
		boundary := unicode.IsLower(prev) || unicode.IsDigit(prev)
		if !boundary && unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			boundary = true // HTTPServer -> HTTP + Server
		}
		if boundary && i > start {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

// splitWord 拆分下划线/驼峰子词，返回去掉原词本身的子词。
func splitWord(w string) []string {
	var parts []string
	for _, seg := range strings.FieldsFunc(w, func(r rune) bool { return r == '_' || r == '$' }) {
		parts = append(parts, splitCamel(seg)...)
	}
	return parts
}

// Terms 从文本抽取索引词：
//   - 中文（Han）连续片段按 bigram 切分（单字片段保留单字词）；
//   - 英文/数字/代码标识符转小写整词，另加 camelCase/下划线子词；
//   - 全角转半角、去零宽、英文大小写不敏感。
func Terms(text string) []Term {
	weights := map[string]float64{}
	add := func(term string, weight float64) {
		if term == "" {
			return
		}
		if w, ok := weights[term]; !ok || weight > w {
			weights[term] = weight
		}
	}
	nr := normalizeRunes(text)
	var hanBuf, wordBuf []rune
	flushHan := func() {
		if len(hanBuf) == 0 {
			return
		}
		if len(hanBuf) == 1 {
			add(strings.ToLower(string(hanBuf)), weightSingleHan)
		} else {
			for i := 0; i+1 < len(hanBuf); i++ {
				add(string(hanBuf[i:i+2]), weightWord)
			}
		}
		hanBuf = hanBuf[:0]
	}
	flushWord := func() {
		if len(wordBuf) == 0 {
			return
		}
		w := string(wordBuf)
		lower := strings.ToLower(w)
		if len([]rune(lower)) > 64 {
			lower = string([]rune(lower)[:64])
		}
		add(lower, weightWord)
		for _, sub := range splitWord(w) {
			sub = strings.ToLower(sub)
			if len([]rune(sub)) >= 2 && sub != lower {
				add(sub, weightSubword)
			}
		}
		wordBuf = wordBuf[:0]
	}
	for _, r := range nr {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			hanBuf = append(hanBuf, r)
		case isWordRune(r):
			flushHan()
			wordBuf = append(wordBuf, r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()

	out := make([]Term, 0, len(weights))
	for t, w := range weights {
		out = append(out, Term{Text: t, Weight: w})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	if len(out) > maxTermsPerDoc {
		out = out[:maxTermsPerDoc]
	}
	return out
}

// QueryTerms 从查询串抽取查询词（与索引词保持同一套切分规则）。
// 中文 2 字及以上按 bigram 交集；单字回退 LIKE。
func QueryTerms(q string) []QueryTerm {
	seen := map[string]bool{}
	var out []QueryTerm
	add := func(t QueryTerm) {
		if t.Text == "" || seen[t.Text] {
			return
		}
		seen[t.Text] = true
		out = append(out, t)
	}
	nr := normalizeRunes(q)
	var hanBuf, wordBuf []rune
	flushHan := func() {
		if len(hanBuf) == 0 {
			return
		}
		if len(hanBuf) == 1 {
			add(QueryTerm{Text: strings.ToLower(string(hanBuf)), SingleHan: true})
		} else {
			for i := 0; i+1 < len(hanBuf); i++ {
				add(QueryTerm{Text: string(hanBuf[i : i+2])})
			}
		}
		hanBuf = hanBuf[:0]
	}
	flushWord := func() {
		if len(wordBuf) == 0 {
			return
		}
		lower := strings.ToLower(string(wordBuf))
		if len([]rune(lower)) > 64 {
			lower = string([]rune(lower)[:64])
		}
		add(QueryTerm{Text: lower})
		wordBuf = wordBuf[:0]
	}
	for _, r := range nr {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			hanBuf = append(hanBuf, r)
		case isWordRune(r):
			flushHan()
			wordBuf = append(wordBuf, r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()
	return out
}
