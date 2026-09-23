package multisource

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/projection"
)

// Store 是多源合成作业的持久化抽象。并发提交时实现必须保证各行互不串写、
// 不互相覆盖（PostgreSQL 用各自行的 INSERT，天然满足）。
// 方法名带 Multi 后缀，便于同一个 Postgres 连接池同时实现 job.Store 与本接口。
type Store interface {
	SaveMulti(ctx context.Context, rec model.MultiSourceJobRecord) error
	GetMulti(ctx context.Context, id string) (model.MultiSourceJobRecord, error)
}

// ErrNotFound 表示多源作业标识不存在。
type ErrNotFound struct{ ID string }

func (e ErrNotFound) Error() string { return "多源合成作业不存在: " + e.ID }

// FieldError 是结构化字段错误（与 validate 包同形，避免循环依赖）。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e FieldError) Error() string { return e.Field + ": " + e.Reason }

// Errors 聚合多个字段错误，实现 error。
type Errors []FieldError

func (es Errors) Error() string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

func nonFinite(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }

// ValidateJob 校验多源作业级字段（风向角、风速、稳定度、源批/受体批非空），
// 并把稳定度归一化后回写。源级、受体级非法不在此拦截——编排层逐项隔离：
// 非法源按零贡献处理并说明原因，非法受体单独标 invalid，均不拖垮整批。
func ValidateJob(req *model.MultiSourceRequest) error {
	var errs Errors
	if err := projection.ValidAngle(req.WindAngleDeg); err != nil {
		errs = append(errs, FieldError{"wind_angle_deg", err.Error()})
	}
	if nonFinite(req.U) {
		errs = append(errs, FieldError{"u", "风速必须为有限数值"})
	} else if req.U <= 0 {
		errs = append(errs, FieldError{"u", fmt.Sprintf("风速 u=%g 必须为正", req.U)})
	}
	req.Stability = req.Stability.Normalize()
	if !req.Stability.Valid() {
		errs = append(errs, FieldError{"stability", fmt.Sprintf("稳定度类别 %q 不在 A–F 之间", req.Stability)})
	}
	if len(req.Sources) == 0 {
		errs = append(errs, FieldError{"sources", "点源批不能为空"})
	}
	if len(req.Receptors) == 0 {
		errs = append(errs, FieldError{"receptors", "受体点批不能为空"})
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Service 编排多源合成计算与入库。
type Service struct {
	store Store
	now   func() time.Time
}

// NewService 构造多源作业服务。
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// Run 执行一次多源合成：逐受体点，对每个源算贡献、累加并拆解占比（不落库）。
//
// 隔离保证：单个源非法只影响它自己的贡献（零贡献 + 原因），单个受体坐标
// 非法只影响该受体条目；其余源/受体照常参与，整批绝不因单项出错而报错。
func (s *Service) Run(req model.MultiSourceRequest) model.MultiSourceResult {
	res := model.MultiSourceResult{
		Status:       "ok",
		WindAngleDeg: req.WindAngleDeg,
		U:            req.U,
		Stability:    req.Stability,
		Receptors:    make([]model.ReceptorResult, 0, len(req.Receptors)),
	}
	for i, rec := range req.Receptors {
		rr := model.ReceptorResult{
			Index:        i,
			ReceptorName: rec.Name,
			X:            rec.X,
			Y:            rec.Y,
			Status:       "ok",
		}
		if nonFinite(rec.X) || nonFinite(rec.Y) {
			rr.Status = "invalid"
			rr.Error = "受体水平坐标必须为有限数值"
			res.Status = "partial"
			res.Receptors = append(res.Receptors, rr)
			continue
		}
		contribs := make([]model.SourceContribution, 0, len(req.Sources))
		for j, src := range req.Sources {
			contribs = append(contribs, SourceContribution(src, j, rec, req.WindAngleDeg, req.U, req.Stability))
		}
		total, hasInvalid := Aggregate(contribs)
		rr.TotalC = total
		rr.Contributions = contribs
		if hasInvalid {
			rr.Status = "partial"
			res.Status = "partial"
		}
		res.Receptors = append(res.Receptors, rr)
	}
	return res
}

// Submit 执行多源合成并作为作业落库，返回作业记录（含标识）。
// 作业级字段非法（风向角非有限、风速非正、稳定度非法、源批/受体批为空）
// 整体拒绝，不落库。
func (s *Service) Submit(ctx context.Context, req model.MultiSourceRequest, id string) (model.MultiSourceJobRecord, error) {
	if err := ValidateJob(&req); err != nil {
		return model.MultiSourceJobRecord{}, err
	}
	rec := model.MultiSourceJobRecord{
		ID:        id,
		Request:   req,
		Result:    s.Run(req),
		CreatedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.store.SaveMulti(ctx, rec); err != nil {
		return model.MultiSourceJobRecord{}, err
	}
	return rec, nil
}

// Get 按标识回查历史多源作业（完整输入：每个源的坐标与排放条件；完整结果：
// 每个受体的合成浓度与逐源拆解）。
func (s *Service) Get(ctx context.Context, id string) (model.MultiSourceJobRecord, error) {
	return s.store.GetMulti(ctx, id)
}

// MemoryStore 是用于测试的线程安全内存存储，实现 Store。
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]model.MultiSourceJobRecord
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]model.MultiSourceJobRecord)}
}

// SaveMulti 写入一行；固定 id 已存在时不覆盖（与 Postgres ON CONFLICT DO NOTHING 对齐）。
func (m *MemoryStore) SaveMulti(_ context.Context, rec model.MultiSourceJobRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rows[rec.ID]; exists {
		return nil
	}
	m.rows[rec.ID] = rec
	return nil
}

// GetMulti 读取一行；不存在返回 ErrNotFound。
func (m *MemoryStore) GetMulti(_ context.Context, id string) (model.MultiSourceJobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[id]
	if !ok {
		return model.MultiSourceJobRecord{}, ErrNotFound{ID: id}
	}
	return rec, nil
}

// Len 返回已存作业数。
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}
