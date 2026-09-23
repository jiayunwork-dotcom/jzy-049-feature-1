// Package multijob 在多点源叠加计算层（internal/multisource）之上负责作业编排：
// 校验作业级公共条件、补齐源/受体缺省标识、执行叠加计算并落库成可回查作业。
//
// 与单源扫描作业（internal/job）完全平行、互不替换：单源计算与扫描接口原样
// 保留，本包只是新长出来的一层。持久化各行独立 INSERT（多源明细表同一事务），
// 多个多源作业并发提交互不写串。
package multijob

import (
	"context"
	"fmt"
	"time"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/multisource"
	"gaussian-plume/internal/validate"
)

// MultiStore 是多源叠加作业的持久化抽象。实现必须保证并发提交时各作业
// （主行 + 源明细行）互不串写、不互相覆盖。
type MultiStore interface {
	SaveMulti(ctx context.Context, rec model.MultiSourceJobRecord) error
	GetMulti(ctx context.Context, id string) (model.MultiSourceJobRecord, error)
}

// ErrNotFound 表示多源作业标识不存在。
type ErrNotFound struct{ ID string }

func (e ErrNotFound) Error() string { return "多源叠加作业不存在: " + e.ID }

// Service 编排多源叠加计算与入库。
type Service struct {
	store MultiStore
	now   func() time.Time
}

// NewService 构造多源作业服务。
func NewService(store MultiStore) *Service {
	return &Service{store: store, now: time.Now}
}

// ensureIDs 给没带标识的源/受体补稳定可读的缺省标识（按提交序号），
// 让回查与占比明细能定位到具体哪一根烟囱。
func ensureIDs(req *model.MultiSourceRequest) {
	for i := range req.Sources {
		if req.Sources[i].ID == "" {
			req.Sources[i].ID = fmt.Sprintf("source-%d", i)
		}
	}
	for i := range req.Receptors {
		if req.Receptors[i].ID == "" {
			req.Receptors[i].ID = fmt.Sprintf("receptor-%d", i)
		}
	}
}

// Run 执行一次多源叠加计算（不落库）。作业级公共条件非法时整体报错。
func (s *Service) Run(req model.MultiSourceRequest) (model.MultiSourceResult, error) {
	if err := validate.MultiSource(&req); err != nil {
		return model.MultiSourceResult{}, err
	}
	ensureIDs(&req)
	return multisource.Run(req), nil
}

// Submit 执行多源叠加计算并作为作业落库，返回作业记录（含标识）。
// 个别源非法只导致该源零贡献（status=partial），不影响整单落库。
func (s *Service) Submit(ctx context.Context, req model.MultiSourceRequest, id string) (model.MultiSourceJobRecord, error) {
	// 作业级公共条件非法（风速非正、稳定度非法、风向角非有限、列表为空等）
	// 整体拒绝，不落库；源级排放条件非法不在这里拦截。
	if err := validate.MultiSource(&req); err != nil {
		return model.MultiSourceJobRecord{}, err
	}
	ensureIDs(&req)
	res := multisource.Run(req)
	rec := model.MultiSourceJobRecord{
		ID:        id,
		Request:   req,
		Result:    res,
		CreatedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.store.SaveMulti(ctx, rec); err != nil {
		return model.MultiSourceJobRecord{}, err
	}
	return rec, nil
}

// Get 按标识回查历史多源作业（完整输入，含每个源各自的参数）。
func (s *Service) Get(ctx context.Context, id string) (model.MultiSourceJobRecord, error) {
	return s.store.GetMulti(ctx, id)
}
