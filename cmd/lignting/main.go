package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/memory"
	"github.com/lignting/lignting-agents/internal/platform"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/internal/tools"
)

func main() {
	var (
		dataDir = flag.String("data-dir", ".lignting-data", "数据目录")
		addr    = flag.String("addr", "", "监听地址，默认 127.0.0.1:8787")
		mock    = flag.Bool("mock-model", false, "使用确定性 mock 模型（测试用）")
		version = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()
	if *version {
		fmt.Println("lignting " + config.Version)
		return
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *mock {
		cfg.ModelMode = config.ModelMock
	}
	if err := cfg.Save(); err != nil {
		log.Fatalf("保存配置失败: %v", err)
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	writeSvc, gitW, err := setupWorkspace(ctx, cfg, st)
	if err != nil {
		log.Fatalf("初始化共享写工具失败: %v", err)
	}
	if gitW != nil {
		defer gitW.Stop()
	}
	if report, err := writeSvc.Recover(ctx); err != nil {
		log.Printf("写事件恢复失败: %v", err)
	} else if report.Applied+report.Rejected+report.Unknown > 0 {
		log.Printf("写事件恢复: applied=%d rejected=%d unknown=%d", report.Applied, report.Rejected, report.Unknown)
	}

	toolsReg, err := tools.NewRegistry(tools.Options{
		Root:    cfg.WorkspaceDir,
		Writer:  writeSvc,
		History: writeSvc,
		Search:  &deepseekSearchProvider{dataDir: cfg.DataDir},
		Audit:   st,
	})
	if err != nil {
		log.Fatalf("初始化工具注册表失败: %v", err)
	}

	memSvc, err := memory.NewService(memory.Options{Repo: memory.NewStoreRepo(st)})
	if err != nil {
		log.Fatalf("初始化记忆服务失败: %v", err)
	}

	rt, err := core.NewRuntime(core.Options{
		Store:          st,
		Provider:       newDynamicProvider(cfg),
		Memory:         coreMemoryInjector{svc: memSvc},
		Tools:          core.NewToolRegistry(toolExecutor{reg: toolsReg}),
		Model:          cfg.Model,
		OwnerName:      ownerName(ctx, st, cfg),
		ServerWebTools: true,
		TurnTimeout:    180 * time.Second,
		MaxToolRounds:  4,
	})
	if err != nil {
		log.Fatalf("初始化 Agent 核心失败: %v", err)
	}
	defer rt.Close()

	srv := platform.New(cfg, st)
	srv.SetMessageSink(coreSink{rt: rt})
	orgSvc := core.NewOrgService(st, rt)
	srv.SetOrgService(orgSvc)
	wireActions(rt, orgSvc, st)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Lignting Agents %s 已启动: http://%s", config.Version, cfg.Addr)
		log.Printf("数据目录: %s（模型模式 %s）", cfg.DataDir, cfg.ModelMode)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("正在停止…")
	rt.Close()
	if gitW != nil {
		gitW.Stop()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Println("已停止")
}

// coreSink 只负责把平台已提交的用户消息投递给 core；路由/触发在 core 内决定。
type coreSink struct{ rt *core.Runtime }

func (s coreSink) OnUserMessage(ctx context.Context, msg domain.Message) {
	if err := s.rt.EnqueueUserMessage(ctx, msg); err != nil {
		log.Printf("core 接收用户消息失败: %v", err)
	}
}

func ownerName(ctx context.Context, st *store.Store, cfg config.Config) string {
	o, err := st.GetOwner(ctx)
	if err == nil && o != nil && o.Name != "" {
		return o.Name
	}
	return cfg.OwnerName
}
