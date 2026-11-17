package domain

import "fmt"

// Error 是 API/核心统一错误模型。Code 稳定，Message 面向用户。
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Status  int            `json:"-"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func NewError(code, msg string, status int) *Error {
	return &Error{Code: code, Message: msg, Status: status}
}

const (
	CodeInvalid       = "invalid"
	CodeNotFound      = "not_found"
	CodeForbidden     = "forbidden"
	CodeConflict      = "conflict"
	CodeUnauthorized  = "unauthorized"
	CodeUnavailable   = "unavailable"
	CodeBudgetBlocked = "budget_blocked"
	CodeRateLimited   = "rate_limited"
)
