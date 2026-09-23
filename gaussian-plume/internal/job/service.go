// Package job 负责把“排放条件 + 下风向距离网格”编排成逐点扫描，
// 解析有效源高（可叠 Briggs 抬升）、逐点算浓度并落库成可回查作业。
//
// 编排保证：个别网格点非法只影响该点（给错误说明），其余点照算，互不牵连；
// 作业级字段非法则整体拒绝（交由 validate 层在更上游拦截）。
package job

import (
	"context"
	"time"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/plume"
	"gaussian-plume/internal/rise"
	"gaussian-plume/internal/validate"
)

// Store 是扫描作业的持久化抽象。并发提交时实现必须保证各行互不串写、
// 不互相覆盖（PostgreSQL 用各自行的 INSERT，天然满足）。
type Store interface {
	Save(ctx context.Context, rec model.JobRecord) error
	Get(ctx context.Context, id string) (model.JobRecord, error)
}

// ErrNotFound 表示作业标识不存在。
type ErrNotFound struct{ ID string }

func (e ErrNotFound) Error() string { return "扫描作业不存在: " + e.ID }

// validatePoint 把点位校验结果转成错误说明字符串。
func validatePoint(pt model.ScanPointInput) string { return validate.Point(pt) }

// Service 编排扫描计算与入库。
type Service struct {
	store Store
	now   func() time.Time
}

// NewService 构造作业服务。
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// resolveH 解析作业级有效源高。返回 (H, ΔH)。
func (s *Service) resolveH(req *model.ScanRequest) (float64, float64, error) {
	if req.UseGivenH {
		return req.EffectiveH, 0, nil
	}
	if req.BuoyancyRise != nil {
		h, dh, _, _, err := rise.EffectiveHeight(req.PhysicalH, *req.BuoyancyRise)
		if err != nil {
			return 0, 0, err
		}
		return h, dh, nil
	}
	// 不抬升：物理源高即有效源高。
	return req.PhysicalH, 0, nil
}

// Run 执行一条扫描：逐点计算，返回结果（不落库）。
func (s *Service) Run(req model.ScanRequest) (model.ScanResult, error) {
	h, dh, err := s.resolveH(&req)
	if err != nil {
		return model.ScanResult{}, err
	}
	res := model.ScanResult{EffectiveH: h, DeltaH: dh}
	allOK := true
	res.Points = make([]model.ScanPointResult, 0, len(req.Points))
	for i, pt := range req.Points {
		pr := model.ScanPointResult{Index: i, X: pt.X, Y: pt.Y, Z: pt.Z}
		if msg := validatePoint(pt); msg != "" {
			pr.Error = msg
			allOK = false
			res.Points = append(res.Points, pr)
			continue
		}
		out, err := plume.Calculate(model.PlumeParams{
			Q:          req.Q,
			EffectiveH: h,
			U:          req.U,
			Stability:  req.Stability,
			X:          pt.X,
			Y:          pt.Y,
			Z:          pt.Z,
		})
		if err != nil {
			pr.Error = err.Error()
			allOK = false
			res.Points = append(res.Points, pr)
			continue
		}
		c, sy, sz := out.C, out.SigmaY, out.SigmaZ
		pr.C, pr.SigmaY, pr.SigmaZ = &c, &sy, &sz
		res.Points = append(res.Points, pr)
	}
	if allOK {
		res.Status = "ok"
	} else {
		res.Status = "partial"
	}
	return res, nil
}

// Submit 执行扫描并作为作业落库，返回作业记录（含标识）。
func (s *Service) Submit(ctx context.Context, req model.ScanRequest, id string) (model.JobRecord, error) {
	// 作业级字段非法（风速非正、源强为负、稳定度非法、网格空等）整体拒绝，不落库。
	if err := validate.Scan(&req); err != nil {
		return model.JobRecord{}, err
	}
	res, err := s.Run(req)
	if err != nil {
		return model.JobRecord{}, err
	}
	rec := model.JobRecord{
		ID:        id,
		Request:   req,
		Result:    res,
		CreatedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.store.Save(ctx, rec); err != nil {
		return model.JobRecord{}, err
	}
	return rec, nil
}

// Get 按标识回查历史作业。
func (s *Service) Get(ctx context.Context, id string) (model.JobRecord, error) {
	return s.store.Get(ctx, id)
}
