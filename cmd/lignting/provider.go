package main

import (
	"context"
	"strings"
	"sync"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/deepseek"
)

// dynamicProvider 在每次调用前读取 secrets.json，用户在设置页更新 API Key 后
// 不需要重启进程；mock 模式固定使用确定性 provider。
type dynamicProvider struct {
	cfg     config.Config
	dataDir string

	mu     sync.Mutex
	key    string
	client *deepseek.Client
	mock   *deepseek.Mock
}

func newDynamicProvider(cfg config.Config) *dynamicProvider {
	return &dynamicProvider{cfg: cfg, dataDir: cfg.DataDir}
}

func (p *dynamicProvider) Name() string {
	if p.cfg.ModelMode == config.ModelMock {
		return "mock"
	}
	return "deepseek"
}

func (p *dynamicProvider) current() deepseek.Provider {
	if p.cfg.ModelMode == config.ModelMock {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.mock == nil {
			p.mock = deepseek.NewMock()
		}
		return p.mock
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	secrets, _ := config.LoadSecrets(p.dataDir)
	key := strings.TrimSpace(secrets.DeepSeekAPIKey)
	if p.client == nil || key != p.key {
		p.key = key
		p.client = deepseek.New(deepseek.Config{APIKey: key, Model: p.cfg.Model, MaxRetries: 2})
	}
	return p.client
}

func (p *dynamicProvider) Complete(ctx context.Context, req deepseek.Request) (deepseek.Response, error) {
	return p.current().Complete(ctx, req)
}

func (p *dynamicProvider) Stream(ctx context.Context, req deepseek.Request, sink deepseek.Sink) (deepseek.Response, error) {
	return p.current().Stream(ctx, req, sink)
}
