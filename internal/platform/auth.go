package platform

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

const (
	sessionCookieName = "lignting_session"
	csrfHeaderName    = "X-Lignting-CSRF"
	sessionTTL        = 30 * 24 * time.Hour
	sessionSecretKey  = "session.secret"
	csrfPurpose       = "csrf:"
)

// sessionKey 懒加载/生成 session 签名密钥；密钥只存 settings，不回显、不进日志。
func (s *Server) sessionKey() ([]byte, error) {
	s.secretMu.Lock()
	defer s.secretMu.Unlock()
	if len(s.secret) == 32 {
		return s.secret, nil
	}
	ctx, cancel := s.opCtx()
	defer cancel()
	v, ok, err := s.store.Setting(ctx, sessionSecretKey)
	if err != nil {
		return nil, err
	}
	var key []byte
	if ok {
		key, err = hex.DecodeString(v)
		if err != nil || len(key) != 32 {
			key = nil
		}
	}
	if key == nil {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		key = b
		if err := s.store.SetSetting(ctx, sessionSecretKey, hex.EncodeToString(b)); err != nil {
			return nil, err
		}
	}
	s.secret = key
	return key, nil
}

// generateSecret 生成新 session 密钥（setup 事务内写入 settings）。
func generateSecret() ([]byte, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	return b, hex.EncodeToString(b), nil
}

func (s *Server) useSecret(key []byte) {
	s.secretMu.Lock()
	s.secret = key
	s.secretMu.Unlock()
}

func (s *Server) issueSession(w http.ResponseWriter, ownerID string) error {
	key, err := s.sessionKey()
	if err != nil {
		return err
	}
	exp := time.Now().Add(sessionTTL).Unix()
	payload := ownerID + "|" + strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	token := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
		Expires:  time.Now().Add(sessionTTL),
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (s *Server) verifySessionToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	key, err := s.sessionKey()
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	fields := strings.SplitN(string(payload), "|", 2)
	if len(fields) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return fields[0], true
}

// currentOwner 从 Cookie 解析当前登录者；未登录返回 (nil, nil)。
func (s *Server) currentOwner(r *http.Request) (*store.Owner, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, nil
	}
	ownerID, ok := s.verifySessionToken(c.Value)
	if !ok {
		return nil, nil
	}
	owner, err := s.store.GetOwner(r.Context())
	if err != nil {
		return nil, err
	}
	if owner == nil || owner.ID != ownerID {
		return nil, nil
	}
	return owner, nil
}

func (s *Server) csrfToken(ownerID string) (string, error) {
	key, err := s.sessionKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(csrfPurpose + ownerID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) verifyCSRF(r *http.Request, owner *store.Owner) error {
	got := r.Header.Get(csrfHeaderName)
	if got == "" {
		return errForbidden("缺少 X-Lignting-CSRF 请求头")
	}
	want, err := s.csrfToken(owner.ID)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return errForbidden("CSRF 令牌无效")
	}
	return nil
}

type authedHandler func(http.ResponseWriter, *http.Request, *store.Owner)

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// authed 做会话校验；写方法额外做 CSRF 校验。
func (s *Server) authed(fn authedHandler) http.HandlerFunc {
	return s.authedWithCSRF(fn, true)
}

// authedNoCSRF 用于 logout 这类无害写操作。
func (s *Server) authedNoCSRF(fn authedHandler) http.HandlerFunc {
	return s.authedWithCSRF(fn, false)
}

func (s *Server) authedWithCSRF(fn authedHandler, requireCSRF bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, err := s.currentOwner(r)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if owner == nil {
			s.writeErr(w, errUnauthorized("请先登录（首次使用请完成初始化）"))
			return
		}
		if requireCSRF && !isSafeMethod(r.Method) {
			if err := s.verifyCSRF(r, owner); err != nil {
				s.writeErr(w, err)
				return
			}
		}
		fn(w, r, owner)
	}
}

// requireLoopback 判断请求是否来自本机（免密自动登录仅限 loopback）。
func requireLoopback(r *http.Request) bool {
	host := hostOnly(r.RemoteAddr)
	if host == "" {
		return false
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

var _ = domain.CodeForbidden
