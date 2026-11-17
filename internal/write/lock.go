package write

import (
	"context"
	"sync"
)

type semaphore chan struct{}

func newSemaphore() semaphore {
	s := make(semaphore, 1)
	s <- struct{}{}
	return s
}

func (s semaphore) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s:
		return nil
	}
}

func (s semaphore) release() { s <- struct{}{} }

type keyedLocks struct {
	mu sync.Mutex
	m  map[string]*keyedLock
}

type keyedLock struct {
	sem  semaphore
	refs int
}

func newKeyedLocks() *keyedLocks { return &keyedLocks{m: make(map[string]*keyedLock)} }

func (k *keyedLocks) acquire(ctx context.Context, key string) (func(), error) {
	k.mu.Lock()
	e := k.m[key]
	if e == nil {
		e = &keyedLock{sem: newSemaphore()}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()

	if err := e.sem.acquire(ctx); err != nil {
		k.releaseRef(key)
		return nil, err
	}
	return func() {
		e.sem.release()
		k.releaseRef(key)
	}, nil
}

func (k *keyedLocks) releaseRef(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e := k.m[key]
	if e == nil {
		return
	}
	e.refs--
	if e.refs <= 0 {
		delete(k.m, key)
	}
}
