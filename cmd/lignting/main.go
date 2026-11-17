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
	"github.com/lignting/lignting-agents/internal/platform"
	"github.com/lignting/lignting-agents/internal/store"
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

	srv := platform.New(cfg, st)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Lignting Agents %s 已启动: http://%s", config.Version, cfg.Addr)
		log.Printf("数据目录: %s", cfg.DataDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Println("已停止")
}
