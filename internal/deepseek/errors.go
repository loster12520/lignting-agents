package deepseek

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNoAPIKey 表示未配置 DeepSeek API Key。
// 调用方应当降级而不是崩溃（见 requirements FR-ONB-06 / NFR-REL-07）。
var ErrNoAPIKey = errors.New("deepseek: 未配置 API Key")

// APIError 是 DeepSeek 返回的非 2xx 错误。
type APIError struct {
	StatusCode int
	Code       string
	Type       string
	Message    string
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	code := e.Code
	if code == "" {
		code = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("deepseek: HTTP %d %s: %s", e.StatusCode, code, e.Message)
}

// Retryable 表示该错误是否值得重试：仅限 408/429/5xx。
func (e *APIError) Retryable() bool {
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// TransportError 是网络/读写层错误（可重试，除非上下文已取消）。
type TransportError struct {
	Op  string
	Err error
}

func (e *TransportError) Error() string { return fmt.Sprintf("deepseek: %s: %v", e.Op, e.Err) }
func (e *TransportError) Unwrap() error { return e.Err }

// IsRetryable 判断错误是否可重试。上下文取消/超时不再重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	var transErr *TransportError
	if errors.As(err, &transErr) {
		if errors.Is(transErr.Err, context.Canceled) {
			return false
		}
		return true
	}
	return false
}

// IsNoAPIKey 判断是否为缺少 Key。
func IsNoAPIKey(err error) bool { return errors.Is(err, ErrNoAPIKey) }

// IsRateLimited 判断是否为限流错误（429）。
func IsRateLimited(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

// IsContextWindowExceeded 判断是否为上下文超限（400 且消息包含关键字）。
// DeepSeek 目前用 400 返回上下文超限；这里做保守识别，便于 core 触发压缩/截断。
func IsContextWindowExceeded(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	msg := apiErr.Message + " " + apiErr.Body
	return containsAny(msg, "context length", "context_length", "maximum context", "too long")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && containsFold(s, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	// 避免引入 strings 的重复逻辑；这里简单大小写折叠。
	return len(sub) <= len(s) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		match := true
		for j := 0; j < n; j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
