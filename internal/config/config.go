package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.0.1"

const (
	ModelDeepSeek = "deepseek"
	ModelMock     = "mock"
	ModelFlash    = "deepseek-flash"
)

type Config struct {
	DataDir            string  `json:"data_dir"`
	Addr               string  `json:"addr"`
	ModelMode          string  `json:"model_mode"`
	Model              string  `json:"model"`
	WorkspaceDir       string  `json:"workspace_dir"`
	OwnerName          string  `json:"owner_name"`
	Locale             string  `json:"locale"`
	DailyBudgetCNY     float64 `json:"daily_budget_cny"`
	MonthlyBudgetCNY   float64 `json:"monthly_budget_cny"`
	MaxAgents          int     `json:"max_agents"`
	WriteTimelineLimit int     `json:"write_timeline_limit"`
	ParallelByFile     bool    `json:"write_parallel_by_file"`
	CreatedAt          string  `json:"created_at"`
}

type Secrets struct {
	DeepSeekAPIKey string `json:"deepseek_api_key"`
}

func Default(dataDir string) Config {
	if dataDir == "" {
		dataDir = ".lignting-data"
	}
	abs, err := filepath.Abs(dataDir)
	if err == nil {
		dataDir = abs
	}
	return Config{
		DataDir:            dataDir,
		Addr:               "127.0.0.1:8787",
		ModelMode:          ModelDeepSeek,
		Model:              ModelFlash,
		WorkspaceDir:       filepath.Join(dataDir, "workspace"),
		OwnerName:          "项目主管",
		Locale:             "zh-CN",
		DailyBudgetCNY:     20,
		MonthlyBudgetCNY:   300,
		MaxAgents:          20,
		WriteTimelineLimit: 30,
		ParallelByFile:     false,
		CreatedAt:          "",
	}
}

func (c Config) ConfigPath() string { return filepath.Join(c.DataDir, "config.json") }
func (c Config) DBPath() string     { return filepath.Join(c.DataDir, "lignting.db") }
func (c Config) SecretsPath() string { return filepath.Join(c.DataDir, "secrets.json") }

func Load(dataDir string) (Config, error) {
	cfg := Default(dataDir)
	if err := cfg.EnsureDirs(); err != nil {
		return cfg, err
	}
	b, err := os.ReadFile(cfg.ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return cfg, cfg.Save()
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = dataDir
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8787"
	}
	if cfg.Model == "" {
		cfg.Model = ModelFlash
	}
	if cfg.ModelMode == "" {
		cfg.ModelMode = ModelDeepSeek
	}
	if cfg.WriteTimelineLimit <= 0 {
		cfg.WriteTimelineLimit = 30
	}
	if cfg.MaxAgents <= 0 {
		cfg.MaxAgents = 20
	}
	return cfg, cfg.EnsureDirs()
}

func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.WorkspaceDir, filepath.Join(c.DataDir, "artifacts"), filepath.Join(c.DataDir, "backups"), filepath.Join(c.DataDir, "logs")} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

func (c Config) Save() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.ConfigPath(), b, 0o600)
}

func LoadSecrets(dataDir string) (Secrets, error) {
	var s Secrets
	b, err := os.ReadFile(filepath.Join(dataDir, "secrets.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("parse secrets: %w", err)
	}
	return s, nil
}

func SaveSecrets(dataDir string, s Secrets) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dataDir, "secrets.json"), b, 0o600)
}

// Redact 从任意文本里去掉敏感值，用于日志/导出。
func (s Secrets) Redact(text string) string {
	if s.DeepSeekAPIKey == "" {
		return text
	}
	return strings.ReplaceAll(text, s.DeepSeekAPIKey, "[REDACTED]")
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
