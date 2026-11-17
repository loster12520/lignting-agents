package platform

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// 统一响应包（api-contract.md §1.1）：成功 {ok:true,data}；失败 {ok:false,error:{code,message}}。
type okEnvelope struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type errorEnvelope struct {
	OK    bool     `json:"ok"`
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, okEnvelope{OK: true, Data: data})
}

func writeList(w http.ResponseWriter, items any, total int64, next string) {
	writeOK(w, map[string]any{"items": items, "total": total, "next": next})
}

func errInvalid(msg string) *domain.Error {
	return domain.NewError(domain.CodeInvalid, msg, http.StatusBadRequest)
}
func errUnauthorized(msg string) *domain.Error {
	return domain.NewError(domain.CodeUnauthorized, msg, http.StatusUnauthorized)
}
func errForbidden(msg string) *domain.Error {
	return domain.NewError(domain.CodeForbidden, msg, http.StatusForbidden)
}
func errNotFound(msg string) *domain.Error {
	return domain.NewError(domain.CodeNotFound, msg, http.StatusNotFound)
}
func errConflict(msg string) *domain.Error {
	return domain.NewError(domain.CodeConflict, msg, http.StatusConflict)
}

// writeErr 把内部错误映射为 HTTP 错误包；不泄露内部堆栈。
func (s *Server) writeErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if errors.As(err, &de) {
		status := de.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errorEnvelope{OK: false, Error: apiError{Code: de.Code, Message: de.Message, Details: de.Details}})
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorEnvelope{OK: false, Error: apiError{Code: domain.CodeNotFound, Message: "对象不存在或已删除"}})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, errorEnvelope{OK: false, Error: apiError{Code: domain.CodeConflict, Message: "状态冲突，请刷新后重试"}})
	case errors.Is(err, store.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, errorEnvelope{OK: false, Error: apiError{Code: domain.CodeInvalid, Message: "请求参数不合法"}})
	default:
		s.logf("internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{OK: false, Error: apiError{Code: "internal", Message: "服务内部错误，请查看日志"}})
	}
}
