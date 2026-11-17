package memory

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Options 是构造 Service 的全部依赖。Repo 必填；Clock 缺省用 SystemClock；
// Config 零值会套用 DefaultConfig；Extractor/Compressor 可为 nil（对应层降级）。
type Options struct {
	Repo       Repo
	Clock      Clock
	Config     Config
	Extractor  Extractor
	Compressor Compressor
	Logger     *slog.Logger
}

// Service 是四层记忆的门面。所有方法并发安全：内部只共享不可变配置、
// Repo（由实现保证并发安全）、gate（自带锁）与 pending 队列（自带锁）。
type Service struct {
	repo       Repo
	clock      Clock
	cfg        Config
	extractor  Extractor
	compressor Compressor
	log        *slog.Logger
	gate       *gate

	mu      sync.Mutex
	pending []compressionJob
}

// NewService 构造记忆服务。
func NewService(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, invalidError("memory: Repo 不能为空", nil)
	}
	cfg := opts.Config.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, invalidError(err.Error(), nil)
	}
	clock := opts.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		repo:       opts.Repo,
		clock:      clock,
		cfg:        cfg,
		extractor:  opts.Extractor,
		compressor: opts.Compressor,
		log:        logger,
		gate:       newGate(clock, time.Duration(cfg.CompressCooldownSecond)*time.Second, time.Hour),
	}, nil
}

// Config 返回归一化后的配置副本。
func (s *Service) Config() Config { return s.cfg }

// Enabled 报告记忆层是否开启（四层可分别开关的总开关）。
func (s *Service) Enabled() bool { return s.cfg.Enabled }

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

func (s *Service) stamp() string { return s.now().Format(time.RFC3339Nano) }

// writeDetail 把结构化细节编码成事件 JSON；序列化失败退化为空对象。
func writeDetail(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (s *Service) event(ctx context.Context, memoryID, action string, actor Access, detail any) {
	ev := MemoryEvent{
		MemoryID:  memoryID,
		Action:    action,
		ActorType: actor.Type,
		ActorID:   actor.ID,
		Detail:    writeDetail(detail),
		CreatedAt: s.stamp(),
	}
	if err := s.repo.AppendMemoryEvent(ctx, ev); err != nil {
		// 审计失败不能打断主流程，但必须留下日志（FR-MEM-11）。
		s.log.Warn("memory: append event failed", "memory_id", memoryID, "action", action, "error", err)
	}
}

// getRaw 读取档案；仓储未命中统一映射为 domain not_found。
func (s *Service) getRaw(ctx context.Context, id string) (domain.Memory, error) {
	m, err := s.repo.GetMemory(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.Memory{}, notFoundError("记忆不存在")
		}
		return domain.Memory{}, err
	}
	return m, nil
}
