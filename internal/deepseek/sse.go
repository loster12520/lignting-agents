package deepseek

import (
	"bufio"
	"bytes"
	"io"
)

const (
	maxSSELineBytes  = 4 << 20
	sseInitialBuffer = 64 << 10
)

// sseScanner 按 SSE 规则把字节流拆成 data 事件（以空行分隔）。
// 只关心 data 行；event/id/retry 与注释行被忽略。
type sseScanner struct {
	sc *bufio.Scanner
}

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, sseInitialBuffer), maxSSELineBytes)
	return &sseScanner{sc: sc}
}

// next 返回下一个 data 事件；ok=false 表示流结束。
func (s *sseScanner) next() (data []byte, ok bool, err error) {
	var buf []byte
	for s.sc.Scan() {
		line := s.sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			if len(buf) > 0 {
				return buf, true, nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			v := line[len("data:"):]
			v = bytes.TrimPrefix(v, []byte(" "))
			if len(buf) > 0 {
				buf = append(buf, '\n')
			}
			buf = append(buf, v...)
		}
	}
	if err := s.sc.Err(); err != nil {
		return nil, false, err
	}
	if len(buf) > 0 {
		return buf, true, nil
	}
	return nil, false, nil
}
