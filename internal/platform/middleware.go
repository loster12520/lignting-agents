package platform

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Handler 组装最外层中间件：panic 恢复 -> 安全头 -> Host/Origin/路径校验 -> mux。
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.securityHeaders(s.guard(s.mux)))
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logf("panic: %v\n%s", rec, debug.Stack())
				writeJSON(w, http.StatusInternalServerError, errorEnvelope{OK: false, Error: apiError{Code: "internal", Message: "服务内部错误"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS rebinding / 跨站防护：Host 必须是 loopback 或显式配置的监听主机。
		if !s.hostAllowed(r.Host) {
			s.writeErr(w, errForbidden("Host 不被允许（默认仅本机访问）"))
			return
		}
		if err := s.originAllowed(r); err != nil {
			s.writeErr(w, err)
			return
		}
		// 目录穿越：在 ServeMux 归一化/重定向之前直接 404，避免 307 暴露路径。
		if dangerousPath(r) {
			s.writeErr(w, errNotFound("路径不存在"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func hostOnly(hostport string) string {
	if hostport == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.ToLower(strings.Trim(h, "[]"))
	}
	return strings.ToLower(strings.Trim(hostport, "[]"))
}

func isLoopbackHost(h string) bool {
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func (s *Server) hostAllowed(hostport string) bool {
	h := hostOnly(hostport)
	if h == "" {
		return false
	}
	if isLoopbackHost(h) {
		return true
	}
	// 用户显式配置了非 loopback 监听地址时，允许该主机名。
	return h == hostOnly(s.cfg.Addr)
}

func (s *Server) originAllowed(r *http.Request) error {
	for _, origin := range r.Header.Values("Origin") {
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return errForbidden("Origin 不被允许")
		}
		if !s.hostAllowed(u.Host) || !strings.EqualFold(hostOnly(u.Host), hostOnly(r.Host)) {
			return errForbidden("Origin 与 Host 不一致，已拒绝跨站请求")
		}
	}
	return nil
}

// dangerousPath 检查原始/转义/解码后的路径是否包含目录穿越。
func dangerousPath(r *http.Request) bool {
	suspicious := func(p string) bool {
		p = strings.ToLower(p)
		return strings.Contains(p, "..") ||
			strings.Contains(p, "\\") ||
			strings.Contains(p, "%2e") ||
			strings.Contains(p, "%2f") && strings.Contains(p, "%2e")
	}
	if suspicious(r.URL.Path) || suspicious(r.URL.EscapedPath()) || suspicious(r.RequestURI) {
		return true
	}
	if decoded, err := url.PathUnescape(r.URL.EscapedPath()); err == nil && suspicious(decoded) {
		return true
	}
	return false
}

// assetAllowed 限定静态资源扩展名，避免把源码/配置等文件经 /assets/ 暴露。
func assetAllowed(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".mjs", ".css", ".html", ".svg", ".png", ".jpg", ".jpeg", ".webp",
		".gif", ".ico", ".woff", ".woff2", ".map", ".txt", ".json":
		return true
	}
	return false
}

var _ = fmt.Sprintf
