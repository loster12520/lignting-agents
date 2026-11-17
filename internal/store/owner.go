package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"crypto/pbkdf2"

	"github.com/lignting/lignting-agents/internal/domain"
)

// OwnerID 是 v0.0.1 单用户唯一身份 ID（固定值，便于消息 sender_id 稳定）。
const OwnerID = "owner"

const pbkdf2Iterations = 120000

// Owner 是本地唯一登录者。密码/令牌只存哈希，JSON 永不外泄。
type Owner struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	TokenHash    string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

// HasPassword 表示用户是否设置过本地密码。
func (o Owner) HasPassword() bool { return o.PasswordHash != "" }

// HashPassword 生成 pbkdf2-sha256 密码哈希（本地密码可选）。
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", fmt.Errorf("pbkdf2: %w", err)
	}
	return "pbkdf2-sha256$" + strconv.Itoa(pbkdf2Iterations) + "$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key), nil
}

// VerifyPassword 常量时间校验密码。
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken 生成一次性明文令牌与其 sha256 哈希（Agent Token 用）。
func NewToken() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "lgt_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(sum[:]), nil
}

// HashToken 返回令牌的 sha256 十六进制哈希。
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

const ownerColumns = "id, name, password_hash, token_hash, created_at"

func scanOwner(row interface{ Scan(...any) error }) (Owner, error) {
	var o Owner
	err := row.Scan(&o.ID, &o.Name, &o.PasswordHash, &o.TokenHash, &o.CreatedAt)
	if err != nil {
		return Owner{}, err
	}
	return o, nil
}

// GetOwner 返回唯一 owner；未初始化时返回 (nil, nil)。
func (s *Store) GetOwner(ctx context.Context) (*Owner, error) {
	o, err := scanOwner(s.DB.QueryRowContext(ctx, "SELECT "+ownerColumns+" FROM owner ORDER BY created_at LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get owner: %w", err)
	}
	return &o, nil
}

// SetupComplete 表示首次向导是否已完成。
func (s *Store) SetupComplete(ctx context.Context) (bool, error) {
	o, err := s.GetOwner(ctx)
	return o != nil, err
}

// CreateOwnerTx 在调用方事务内创建 owner（首次 setup 与其他表原子写入）。
func CreateOwnerTx(tx *sql.Tx, o Owner) error {
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("%w: owner name required", ErrInvalid)
	}
	if o.ID == "" {
		o.ID = OwnerID
	}
	if o.CreatedAt == "" {
		o.CreatedAt = Now()
	}
	if _, err := tx.Exec("INSERT INTO owner(id, name, password_hash, token_hash, created_at) VALUES(?,?,?,?,?)",
		o.ID, o.Name, o.PasswordHash, o.TokenHash, o.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("%w: owner already exists", ErrConflict)
		}
		return fmt.Errorf("create owner: %w", err)
	}
	return nil
}

// CreateOwner 创建 owner（密码可为空；为空表示本地免密，仅 loopback 自动登录）。
func (s *Store) CreateOwner(ctx context.Context, name, password string) (*Owner, error) {
	hash := ""
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		hash = h
	}
	o := Owner{ID: OwnerID, Name: strings.TrimSpace(name), PasswordHash: hash, CreatedAt: Now()}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return CreateOwnerTx(tx, o) }); err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateOwnerName 修改 owner 显示名。
func (s *Store) UpdateOwnerName(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: empty owner name", ErrInvalid)
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE owner SET name=? WHERE id=?", name, OwnerID)
	if err != nil {
		return fmt.Errorf("update owner: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: owner", ErrNotFound)
	}
	return nil
}

// SetOwnerPassword 设置/清空本地密码（空字符串表示清除）。
func (s *Store) SetOwnerPassword(ctx context.Context, password string) error {
	hash := ""
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return err
		}
		hash = h
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE owner SET password_hash=? WHERE id=?", hash, OwnerID); err != nil {
		return fmt.Errorf("set owner password: %w", err)
	}
	return nil
}

// TouchOwnerLogin 记录最近登录时间（审计在平台层写）。
func (s *Store) TouchOwnerLogin(ctx context.Context) error {
	_ = ctx
	return nil
}

// OwnerCreatedAtOrZero 便于 bootstrap 输出。
func OwnerCreatedAtOrZero(o *Owner) string {
	if o == nil {
		return ""
	}
	return o.CreatedAt
}

var _ = domain.NewID // owner.go 与 domain 保持同包依赖方向（domain 无回依赖）
