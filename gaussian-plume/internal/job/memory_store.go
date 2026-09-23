package job

import (
	"context"
	"sync"

	"gaussian-plume/internal/model"
)

// MemoryStore 是用于测试的线程安全内存存储，实现 Store。
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]model.JobRecord
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]model.JobRecord)}
}

// Save 写入一行；固定 id 已存在时不覆盖（与 Postgres ON CONFLICT DO NOTHING 对齐）。
func (m *MemoryStore) Save(_ context.Context, rec model.JobRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rows[rec.ID]; exists {
		return nil
	}
	m.rows[rec.ID] = rec
	return nil
}

// Get 读取一行；不存在返回 ErrNotFound。
func (m *MemoryStore) Get(_ context.Context, id string) (model.JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[id]
	if !ok {
		return model.JobRecord{}, ErrNotFound{ID: id}
	}
	return rec, nil
}

// Len 返回已存作业数。
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}
