package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type searchTool struct{ opts Options }

func (t *searchTool) Name() string { return "web_search" }
func (t *searchTool) Description() string {
	return "Search the web through the configured DeepSeek server-side search tool or Search API provider."
}
func (t *searchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "search query"},
			"limit": {"type": "integer", "description": "number of results (default 5, max 20)", "minimum": 1, "maximum": 20}
		},
		"required": ["query"],
		"additionalProperties": false
	}`)
}

type searchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

func (t *searchTool) Invoke(ctx context.Context, req Request) (Result, error) {
	if t.opts.Search == nil {
		return errorResult(req.CallID, t.Name(), ErrUnavailable.Error()+": search provider"), nil
	}
	var args searchArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errorResult(req.CallID, t.Name(), "invalid args: "+err.Error()), nil
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return errorResult(req.CallID, t.Name(), "query is required"), nil
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}
	if args.Limit > 20 {
		args.Limit = 20
	}
	hits, err := t.opts.Search.Search(ctx, args.Query, args.Limit)
	if err != nil {
		return errorResult(req.CallID, t.Name(), "search failed: "+err.Error()), nil
	}
	if len(hits) > args.Limit {
		hits = hits[:args.Limit]
	}
	b, _ := json.MarshalIndent(hits, "", "  ")
	return Result{
		CallID: req.CallID, Name: t.Name(), Content: string(b),
		Meta: map[string]any{"query": args.Query, "count": len(hits)},
	}, nil
}

// HTTPProvider 调用一个兼容 Search API 的 HTTP 端点：POST {query, limit} -> {results:[{title,url,snippet}]}。
// core-dev 可以用它接 DeepSeek 联网搜索网关；纯 chat completions 工具请另实现 SearchProvider。
type HTTPProvider struct {
	Endpoint string
	APIKey   string
	Client   *http.Client
	Timeout  time.Duration
}

func (p *HTTPProvider) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if p.Endpoint == "" {
		return nil, fmt.Errorf("search endpoint is empty")
	}
	client := p.Client
	if client == nil {
		timeout := p.Timeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	body, _ := json.Marshal(map[string]any{"query": query, "limit": limit})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search API status %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var payload struct {
		Results []SearchHit `json:"results"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.Results) == 0 {
		var arr []SearchHit
		if err2 := json.Unmarshal(data, &arr); err2 == nil {
			payload.Results = arr
		} else if err != nil {
			return nil, err
		}
	}
	out := make([]SearchHit, 0, len(payload.Results))
	for _, hit := range payload.Results {
		if strings.TrimSpace(hit.URL) == "" && strings.TrimSpace(hit.Title) == "" {
			continue
		}
		out = append(out, hit)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
