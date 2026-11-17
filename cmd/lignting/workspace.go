package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/gitwatch"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/internal/write"
)

// setupWorkspace 构造共享写工具与 Git 监听。默认工作区不是 git 仓库时自动 init，
// 让变更时间线与 commit 组在不污染用户仓库的前提下可用；失败只降级，不阻塞启动。
func setupWorkspace(ctx context.Context, cfg config.Config, st *store.Store) (*write.Service, *gitwatch.Watcher, error) {
	// 默认工作区在数据目录内，可能嵌套在项目仓库中；git -C 会把父仓库误判为工作区仓库。
	// 因此先确保工作区自身有 .git（仅数据目录内的默认工作区），再启动监听。
	if !hasDotGit(cfg.WorkspaceDir) && insideDir(cfg.WorkspaceDir, cfg.DataDir) {
		if err := initGitRepo(cfg.WorkspaceDir); err != nil {
			log.Printf("工作区 git init 失败（写工具继续）: %v", err)
		}
	}
	var watcher *gitwatch.Watcher
	w, err := gitwatch.New(gitwatch.Options{Dir: cfg.WorkspaceDir, Repository: st})
	if errors.Is(err, gitwatch.ErrNotRepository) {
		if initErr := initGitRepo(cfg.WorkspaceDir); initErr == nil {
			w, err = gitwatch.New(gitwatch.Options{Dir: cfg.WorkspaceDir, Repository: st})
		}
	}
	if err == nil {
		if startErr := w.Start(ctx); startErr == nil {
			watcher = w
		} else {
			log.Printf("Git 监听启动失败（写工具继续，分支保护降级）: %v", startErr)
		}
	} else {
		log.Printf("Git 监听不可用（写工具继续，时间线仍记录）: %v", err)
	}

	opts := write.Options{
		Root:           cfg.WorkspaceDir,
		TimelineLimit:  cfg.WriteTimelineLimit,
		ParallelByFile: cfg.ParallelByFile,
		Repository:     st,
		Audit:          st,
	}
	if watcher != nil {
		opts.Branch = watcher
	}
	svc, err := write.New(opts)
	if err != nil {
		return nil, watcher, err
	}
	return svc, watcher, nil
}

func hasDotGit(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}

func insideDir(child, parent string) bool {
	c, err1 := filepath.Abs(child)
	p, err2 := filepath.Abs(parent)
	if err1 != nil || err2 != nil {
		return false
	}
	c = strings.ToLower(filepath.Clean(c))
	p = strings.ToLower(filepath.Clean(p))
	return c == p || strings.HasPrefix(c, p+string(os.PathSeparator))
}

func initGitRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil
	}
	cmd := exec.Command("git", "init", "-b", "main", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init: %w (%s)", err, string(out))
	}
	return nil
}
