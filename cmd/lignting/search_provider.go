package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/tools"
)

// deepseekSearchProvider 调用 DeepSeek 官方 Search API：
// POST https://api.deepseek.com/v1/tools/search
// 每次调用重新读取 secrets.json，用户在设置页更新 Key 后无需重启。
type deepseekSearchProvider struct {
	dataDir string
	baseURL string
	client  *http.Client
}

func (p *deepseekSearchProvider) Search(ctx context.Context, query string, limit int) ([]tools.SearchHit, error) {
	secrets, err := config.LoadSecrets(p.dataDir)
	if err != nil {
		return nil, fmt.Errorf("读取 DeepSeek API Key 失败: %w", err)
	}
	if strings.TrimSpace(secrets.DeepSeekAPIKey) == "" {
		return nil, fmt.Errorf("未配置 DeepSeek API Key，web_search 不可用")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	body, _ := json.Marshal(map[string]any{
		"query":        query,
		"count":        limit,
		"need_content": false,
	})
	base := strings.TrimRight(p.baseURL, "/")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/tools/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secrets.DeepSeekAPIKey)
	client := p.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("搜索请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("搜索 API 返回 %d: %s", resp.StatusCode, truncateText(string(data), 300))
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Summary string `json:"summary"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("解析搜索响应失败: %w", err)
	}
	out := make([]tools.SearchHit, 0, len(payload.Results))
	for _, r := range payload.Results {
		snippet := r.Snippet
		if strings.TrimSpace(snippet) == "" {
			snippet = r.Summary
		}
		out = append(out, tools.SearchHit{Title: r.Title, URL: r.URL, Snippet: snippet})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
