package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// NewStoreRepo 把 *store.Store 适配成 Repo。唯一职责是把 store 包的哨兵错误
// （store.ErrNotFound / store.ErrConflict）映射成本包的哨兵，让 Service 的
// errors.Is 判断与上层 HTTP 状态码不受存储实现影响。
//
// 依赖方向：memory → store 是方案允许的方向（memory 不反向被 store 依赖）；
// store 侧实现只依赖 domain，不需要 import memory。
func NewStoreRepo(s *store.Store) Repo { return &storeRepo{s: s} }

type storeRepo struct{ s *store.Store }

var _ Repo = (*storeRepo)(nil)

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func (r *storeRepo) InsertMemory(ctx context.Context, m domain.Memory) error {
	return mapStoreError(r.s.InsertMemory(ctx, m))
}

func (r *storeRepo) GetMemory(ctx context.Context, id string) (domain.Memory, error) {
	m, err := r.s.GetMemory(ctx, id)
	return m, mapStoreError(err)
}

func (r *storeRepo) UpdateMemory(ctx context.Context, m domain.Memory, expectedVersion int) error {
	return mapStoreError(r.s.UpdateMemory(ctx, m, expectedVersion))
}

func (r *storeRepo) ListMemories(ctx context.Context, f domain.MemoryFilter) ([]domain.Memory, error) {
	rows, err := r.s.ListMemories(ctx, f)
	return rows, mapStoreError(err)
}

func (r *storeRepo) CountMemories(ctx context.Context, f domain.MemoryFilter) (int, error) {
	n, err := r.s.CountMemories(ctx, f)
	return n, mapStoreError(err)
}

func (r *storeRepo) AppendMemoryEvent(ctx context.Context, ev domain.MemoryEvent) error {
	return mapStoreError(r.s.AppendMemoryEvent(ctx, ev))
}

func (r *storeRepo) AddMemoryRead(ctx context.Context, rec domain.MemoryRead) error {
	return mapStoreError(r.s.AddMemoryRead(ctx, rec))
}

func (r *storeRepo) MaxManualVersion(ctx context.Context, key domain.MemoryManualKey) (int, error) {
	n, err := r.s.MaxManualVersion(ctx, key)
	return n, mapStoreError(err)
}

func (r *storeRepo) ListManuals(ctx context.Context, key domain.MemoryManualKey) ([]domain.PromptManual, error) {
	rows, err := r.s.ListManuals(ctx, key)
	return rows, mapStoreError(err)
}

func (r *storeRepo) GetManual(ctx context.Context, id string) (domain.PromptManual, error) {
	m, err := r.s.GetManual(ctx, id)
	return m, mapStoreError(err)
}

func (r *storeRepo) InsertManual(ctx context.Context, m domain.PromptManual) error {
	return mapStoreError(r.s.InsertManual(ctx, m))
}

func (r *storeRepo) UpdateManualStatus(ctx context.Context, id, status string) error {
	return mapStoreError(r.s.UpdateManualStatus(ctx, id, status))
}
