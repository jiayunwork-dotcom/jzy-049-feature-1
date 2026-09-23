package multijob

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"gaussian-plume/internal/model"
)

func mkReq(q1, q2 float64) model.MultiSourceRequest {
	return model.MultiSourceRequest{
		WindDir: 0, U: 5, Stability: model.StabilityD,
		Sources: []model.PointSourceInput{
			{ID: "A", XY: model.XY{X: 0, Y: 0}, Q: q1, PhysicalH: 30},
			{ID: "B", XY: model.XY{X: 100, Y: 0}, Q: q2, PhysicalH: 60},
		},
		Receptors: []model.ReceptorInput{{ID: "R", XY: model.XY{X: 0, Y: -1000}}},
	}
}

// 作业级公共条件非法（零风速、非法稳定度、空源/受体列表、坏风向角）整体拒绝。
func TestSubmitRejectsJobLevelInvalid(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	badU := mkReq(0.1, 0.1)
	badU.U = 0
	if _, err := svc.Submit(ctx, badU, "j1"); err == nil {
		t.Error("零风速应整单拒绝")
	}
	badStab := mkReq(0.1, 0.1)
	badStab.Stability = "Z"
	if _, err := svc.Submit(ctx, badStab, "j2"); err == nil {
		t.Error("非法稳定度应整单拒绝")
	}
	badDir := mkReq(0.1, 0.1)
	badDir.WindDir = math.NaN()
	if _, err := svc.Submit(ctx, badDir, "j3"); err == nil {
		t.Error("NaN 风向角应整单拒绝")
	}
	empty := mkReq(0.1, 0.1)
	empty.Sources = nil
	if _, err := svc.Submit(ctx, empty, "j4"); err == nil {
		t.Error("空源列表应整单拒绝")
	}
	noRec := mkReq(0.1, 0.1)
	noRec.Receptors = nil
	if _, err := svc.Submit(ctx, noRec, "j5"); err == nil {
		t.Error("空受体列表应整单拒绝")
	}
	if store.Len() != 0 {
		t.Errorf("作业级非法不得落库，got %d 行", store.Len())
	}
}

// 含单个非法源的作业仍提交成功（partial），可回查，且回查能还原每个源输入。
func TestSubmitPartialBadSourcePersistedAndRestored(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	req := mkReq(0.1, -0.5) // 源 B 源强为负
	rec, err := svc.Submit(ctx, req, "job-partial")
	if err != nil {
		t.Fatalf("单个坏源不应拖垮整批提交: %v", err)
	}
	if rec.Result.Status != "partial" {
		t.Fatalf("应 partial，got %s", rec.Result.Status)
	}
	got, err := svc.Get(ctx, "job-partial")
	if err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	// 回查必须还原出两个源各自的原始输入，而不只是一个总浓度数字。
	if len(got.Request.Sources) != 2 {
		t.Fatalf("应回查到 2 个源，got %d", len(got.Request.Sources))
	}
	if got.Request.Sources[0].Q != 0.1 || got.Request.Sources[1].Q != -0.5 {
		t.Errorf("源输入还原错误: %+v", got.Request.Sources)
	}
	if got.Request.Sources[0].ID != "A" || got.Request.Sources[1].ID != "B" {
		t.Errorf("源标识应可回查")
	}
	if len(got.Result.Receptors) != 1 || len(got.Result.Receptors[0].Contributions) != 2 {
		t.Fatalf("应回查到 1 受体 ×2 源的逐项贡献")
	}
	cs := got.Result.Receptors[0].Contributions
	if cs[1].C != 0 || cs[1].Error == "" {
		t.Errorf("坏源回查应为零贡献带原因，got %+v", cs[1])
	}
	if math.Abs(got.Result.Receptors[0].C-cs[0].C) > 1e-15 {
		t.Errorf("合成浓度应只含好源贡献")
	}
}

// 没带标识的源/受体补缺省标识，回查能定位到第几根烟囱。
func TestDefaultIDsAssigned(t *testing.T) {
	svc := NewService(NewMemoryStore())
	req := model.MultiSourceRequest{
		WindDir: 90, U: 3, Stability: model.StabilityC,
		Sources: []model.PointSourceInput{
			{XY: model.XY{Y: 0}, Q: 0.1, PhysicalH: 10},
			{XY: model.XY{Y: 50}, Q: 0.2, PhysicalH: 20},
		},
		Receptors: []model.ReceptorInput{{XY: model.XY{X: -500}}},
	}
	rec, err := svc.Submit(context.Background(), req, "job-ids")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Request.Sources[0].ID != "source-0" || rec.Request.Sources[1].ID != "source-1" {
		t.Errorf("源缺省标识错误: %q,%q", rec.Request.Sources[0].ID, rec.Request.Sources[1].ID)
	}
	if rec.Request.Receptors[0].ID != "receptor-0" {
		t.Errorf("受体缺省标识错误: %q", rec.Request.Receptors[0].ID)
	}
}

// 多个多源作业并发提交：互不写串、可分别回查且结果各自正确。
func TestConcurrentMultiJobsDoNotInterleave(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	const n = 50
	var wg sync.WaitGroup
	ids := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		ids[i] = fmt.Sprintf("mjob-%d", i)
		go func() {
			defer wg.Done()
			q := 0.005 * float64(i+1)
			rec, err := svc.Submit(context.Background(), mkReq(q, q*2), ids[i])
			if err != nil {
				t.Errorf("submit %d: %v", i, err)
				return
			}
			if rec.Result.Status != "ok" {
				t.Errorf("job %d 应 ok，got %s", i, rec.Result.Status)
			}
		}()
	}
	wg.Wait()
	if store.Len() != n {
		t.Fatalf("应落库 %d 条多源作业，got %d", n, store.Len())
	}
	for i := 0; i < n; i++ {
		rec, err := svc.Get(context.Background(), ids[i])
		if err != nil {
			t.Fatalf("回查 %s: %v", ids[i], err)
		}
		qWant := 0.005 * float64(i+1)
		if rec.Request.Sources[0].Q != qWant || rec.Request.Sources[1].Q != 2*qWant {
			t.Errorf("%s 源强被串写: got (%v,%v) want (%v,%v)",
				ids[i], rec.Request.Sources[0].Q, rec.Request.Sources[1].Q, qWant, 2*qWant)
		}
	}
}

// 回查不存在的作业返回 ErrNotFound。
func TestGetMissingJob(t *testing.T) {
	svc := NewService(NewMemoryStore())
	if _, err := svc.Get(context.Background(), "nope"); err == nil {
		t.Error("不存在作业应报错")
	}
}
