package write

import (
	"fmt"
	"strings"
)

const maxDiffCells = 4_000_000

// Diff 生成 line-based unified diff；文件差异过大时返回摘要而不是阻塞写入。
func Diff(path, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	a := splitLines(oldText)
	b := splitLines(newText)
	if len(a)*len(b) > maxDiffCells {
		return fmt.Sprintf("diff omitted: file too large for inline diff (old %d lines, new %d lines)\n", len(a), len(b))
	}
	ops := diffOps(a, b)
	return renderUnified(path, ops)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type opKind byte

const (
	opEqual  opKind = ' '
	opDelete opKind = '-'
	opInsert opKind = '+'
)

type diffOp struct {
	kind opKind
	text string
	oldN int
	newN int
}

func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	w := m + 1
	dp := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i*w+j] = dp[(i+1)*w+j+1] + 1
			} else if dp[(i+1)*w+j] >= dp[i*w+j+1] {
				dp[i*w+j] = dp[(i+1)*w+j]
			} else {
				dp[i*w+j] = dp[i*w+j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	oldN, newN := 1, 1
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{kind: opEqual, text: a[i], oldN: oldN, newN: newN})
			i++
			j++
			oldN++
			newN++
		} else if dp[(i+1)*w+j] >= dp[i*w+j+1] {
			ops = append(ops, diffOp{kind: opDelete, text: a[i], oldN: oldN, newN: 0})
			i++
			oldN++
		} else {
			ops = append(ops, diffOp{kind: opInsert, text: b[j], oldN: 0, newN: newN})
			j++
			newN++
		}
	}
	for i < n {
		ops = append(ops, diffOp{kind: opDelete, text: a[i], oldN: oldN, newN: 0})
		i++
		oldN++
	}
	for j < m {
		ops = append(ops, diffOp{kind: opInsert, text: b[j], oldN: 0, newN: newN})
		j++
		newN++
	}
	return ops
}

func renderUnified(path string, ops []diffOp) string {
	const ctx = 3
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	i := 0
	for i < len(ops) {
		// 找到下一处变更。
		for i < len(ops) && ops[i].kind == opEqual {
			i++
		}
		if i >= len(ops) {
			break
		}
		start := i - ctx
		if start < 0 {
			start = 0
		}
		// 向后扩到变更结束后的 ctx 行。
		end := i
		for end < len(ops) {
			if ops[end].kind != opEqual {
				end++
				continue
			}
			// 看看后面 2*ctx 行内是否还有变更；有则合并为一个 hunk。
			nextChange := -1
			for k := end; k < len(ops) && k <= end+2*ctx; k++ {
				if ops[k].kind != opEqual {
					nextChange = k
					break
				}
			}
			if nextChange < 0 {
				break
			}
			end = nextChange
		}
		tail := end + ctx
		if tail > len(ops) {
			tail = len(ops)
		}
		if tail <= start {
			i = end
			continue
		}
		oldStart, newStart := hunkStart(ops, start)
		oldCount, newCount := hunkCount(ops[start:tail])
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, op := range ops[start:tail] {
			b.WriteByte(byte(op.kind))
			b.WriteString(op.text)
			b.WriteByte('\n')
		}
		i = tail
	}
	return b.String()
}

func hunkStart(ops []diffOp, idx int) (int, int) {
	oldN, newN := 1, 1
	for k := 0; k < idx && k < len(ops); k++ {
		switch ops[k].kind {
		case opEqual:
			oldN++
			newN++
		case opDelete:
			oldN++
		case opInsert:
			newN++
		}
	}
	return oldN, newN
}

func hunkCount(ops []diffOp) (int, int) {
	var oldC, newC int
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			oldC++
			newC++
		case opDelete:
			oldC++
		case opInsert:
			newC++
		}
	}
	return oldC, newC
}
