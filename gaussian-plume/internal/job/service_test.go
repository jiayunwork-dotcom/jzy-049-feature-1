package job

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/plume"
)

func reqWithDistances(xs ...float64) model.ScanRequest {
	pts := make([]model.ScanPointInput, len(xs))
	for i, x := range xs {
		pts[i] = model.ScanPointInput{X: x}
	}
	return model.ScanRequest{
		Q: 0.1, PhysicalH: 0, U: 5, Stability: model.StabilityD,
		Points: pts, UseGivenH: true, EffectiveH: 0,
	}
}

// 批量扫描中个别网格点非法：该点给错误说明，其余照常算完。
func TestRunPartialBadPoints(t *testing.T) {
	svc := NewService(NewMemoryStore())
	res, err := svc.Run(reqWithDistances(100, -5, 1000, 0, 2000))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "partial" {
		t.Fatalf("状态应为 partial，got %s", res.Status)
	}
	if len(res.Points) != 5 {
		t.Fatalf("应返回 5 个点，got %d", len(res.Points))
	}
	if res.Points[0].C == nil || res.Points[0].Error != "" {
		t.Error("点0(100m) 应正常出浓度")
	}
	if res.Points[1].C != nil || res.Points[1].Error == "" {
		t.Error("点1(-5) 应只有错误说明")
	}
	if res.Points[2].C == nil {
		t.Error("点2(1000m) 应照常算出，不受坏点牵连")
	}
	if res.Points[3].Error == "" {
		t.Error("点4(x=0) 应有错误说明")
	}
	if res.Points[4].C == nil {
		t.Error("点4(2000m) 应照常算出")
	}
}

// 合法作业的地面源点必须与解析极限一致。
func TestRunGroundSourceMatchesLimit(t *testing.T) {
	svc := NewService(NewMemoryStore())
	res, _ := svc.Run(reqWithDistances(1000))
	c := *res.Points[0].C
	want := plume.GroundCenterlineLimit(0.1, *res.Points[0].SigmaY, *res.Points[0].SigmaZ, 5)
	if math.Abs(c-want)/want > 1e-12 {
		t.Errorf("扫描点浓度 %v != 解析极限 %v", c, want)
	}
}

// 并发提交多条作业：各自结果不串、不互相覆盖，且可分别回查。
func TestConcurrentSubmissionsDoNotInterleave(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	const n = 50
	var wg sync.WaitGroup
	ids := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		ids[i] = fmt.Sprintf("job-%d", i)
		go func() {
			defer wg.Done()
			q := 0.01 * float64(i+1) // 每条作业不同源强
			req := reqWithDistances(100, 500, 1000)
			req.Q = q
			rec, err := svc.Submit(context.Background(), req, ids[i])
			if err != nil {
				t.Errorf("submit %d: %v", i, err)
				return
			}
			if rec.Result.Points[2].C == nil {
				t.Errorf("job %d 第3点丢失", i)
			}
		}()
	}
	wg.Wait()

	if store.Len() != n {
		t.Fatalf("应落库 %d 条作业，got %d", n, store.Len())
	}
	// 回查每一条：源强与结果必须对得上自己，未被其它作业覆盖。
	for i := 0; i < n; i++ {
		rec, err := svc.Get(context.Background(), ids[i])
		if err != nil {
			t.Fatalf("回查 %s: %v", ids[i], err)
		}
		qWant := 0.01 * float64(i+1)
		if rec.Request.Q != qWant {
			t.Errorf("%s 源强被串写: got %v want %v", ids[i], rec.Request.Q, qWant)
		}
		// 第3点(1000m)浓度应与该作业源强下的解析极限一致。
		got := *rec.Result.Points[2].C
		want := plume.GroundCenterlineLimit(qWant, *rec.Result.Points[2].SigmaY, *rec.Result.Points[2].SigmaZ, 5)
		if math.Abs(got-want)/want > 1e-12 {
			t.Errorf("%s 浓度被串写: %v != %v", ids[i], got, want)
		}
	}
}

// 作业级非法（风速非正）应整体报错，不落库。
func TestSubmitRejectsJobLevelInvalid(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	req := reqWithDistances(100)
	req.U = 0
	if _, err := svc.Submit(context.Background(), req, "x"); err == nil {
		t.Error("风速非正应拒绝整单")
	}
	if store.Len() != 0 {
		t.Error("非法作业不应落库")
	}
}

// 示范作业：地面源、1000m 点落在文档核对值附近，且可幂等预置。
func TestSeedDemoIdempotentAndHandCheck(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	if err := svc.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SeedDemo(ctx); err != nil {
		t.Fatalf("二次预置应幂等: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("幂等预置后应只有 1 条作业，got %d", store.Len())
	}
	rec, err := svc.Get(ctx, DemoJobID)
	if err != nil {
		t.Fatal(err)
	}
	// 找 1000m 点。
	var p1000 *model.ScanPointResult
	for i := range rec.Result.Points {
		if rec.Result.Points[i].X == 1000 {
			p1000 = &rec.Result.Points[i]
		}
	}
	if p1000 == nil || p1000.C == nil {
		t.Fatal("示范作业缺少 1000m 点")
	}
	// 1) 必须等于钉死公式在地面源极限下的解析值。
	want := plume.GroundCenterlineLimit(0.1, *p1000.SigmaY, *p1000.SigmaZ, 5)
	if math.Abs(*p1000.C-want)/want > 1e-12 {
		t.Errorf("示范 1000m 浓度 %v 与解析极限 %v 不符", *p1000.C, want)
	}
	// 2) σy、σz 落在 D 类 P-G 锚定值 70、26，浓度落在文档手算值 3.498e-6。
	if math.Abs(*p1000.SigmaY-70)/70 > 0.02 {
		t.Errorf("σy(1000)=%v 应≈70", *p1000.SigmaY)
	}
	if math.Abs(*p1000.SigmaZ-26)/26 > 0.02 {
		t.Errorf("σz(1000)=%v 应≈26", *p1000.SigmaZ)
	}
	if math.Abs(*p1000.C-3.498e-6)/(3.498e-6) > 0.02 {
		t.Errorf("示范 1000m 浓度 %v 与文档手算值 3.498e-6 偏差过大", *p1000.C)
	}
}
