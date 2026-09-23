package store_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"gaussian-plume/internal/job"
	"gaussian-plume/internal/model"
	"gaussian-plume/internal/multijob"
	"gaussian-plume/internal/store"
)

// dsn 返回测试用 PostgreSQL DSN；未设置 DATABASE_URL（或 DATABASE_URL=none）
// 时跳过本文件，因此普通 `go test ./...` 不依赖外部数据库。
func dsn() string {
	v := os.Getenv("DATABASE_URL")
	if v == "" || v == "none" {
		return ""
	}
	return v
}

func newStore(t *testing.T) *store.Postgres {
	t.Helper()
	d := dsn()
	if d == "" {
		t.Skip("未设置 DATABASE_URL，跳过 PostgreSQL 集成测试")
	}
	ctx := context.Background()
	pg, err := store.New(ctx, d)
	if err != nil {
		t.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	t.Cleanup(pg.Close)
	return pg
}

func scanReq(q float64) model.ScanRequest {
	return model.ScanRequest{
		Q: q, PhysicalH: 0, U: 5, Stability: model.StabilityD,
		Points:    []model.ScanPointInput{{X: 100}, {X: 1000}},
		UseGivenH: true,
	}
}

// 提交后应能按标识回查到完整输入与结果。
func TestPostgresSaveAndGet(t *testing.T) {
	pg := newStore(t)
	svc := job.NewService(pg)
	ctx := context.Background()

	rec, err := svc.Submit(ctx, scanReq(0.07), "it-save-get")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if got.Request.Q != 0.07 {
		t.Errorf("回查源强=%v，期望 0.07", got.Request.Q)
	}
	if len(got.Result.Points) != 2 || got.Result.Points[1].C == nil {
		t.Errorf("回查结果点不完整: %+v", got.Result.Points)
	}
	if _, err := svc.Get(ctx, "missing-id"); err == nil {
		t.Error("不存在的作业应返回错误")
	}
}

// 并发提交若干作业：各行不串、不互相覆盖，且都能各自回查。
func TestPostgresConcurrentSubmissions(t *testing.T) {
	pg := newStore(t)
	svc := job.NewService(pg)
	ctx := context.Background()
	const n = 30
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("it-concurrent-%d", i)
			if _, err := svc.Submit(ctx, scanReq(0.001*float64(i+1)), id); err != nil {
				errs <- err
				return
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发提交出错: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("it-concurrent-%d", i)
		rec, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatalf("回查 %s: %v", id, err)
		}
		if want := 0.001 * float64(i+1); rec.Request.Q != want {
			t.Errorf("%s 源强被串写: got %v want %v", id, rec.Request.Q, want)
		}
	}
}

// 预置示范作业在真实 PostgreSQL 上应可写入并回查（重启持久性由卷保证）。
func TestPostgresSeedDemo(t *testing.T) {
	pg := newStore(t)
	svc := job.NewService(pg)
	ctx := context.Background()
	if err := svc.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Get(ctx, job.DemoJobID)
	if err != nil {
		t.Fatalf("示范作业回查失败: %v", err)
	}
	if rec.Result.Status != "ok" {
		t.Errorf("示范作业状态=%s，期望 ok", rec.Result.Status)
	}
}

func multiReq(q1, q2 float64) model.MultiSourceRequest {
	return model.MultiSourceRequest{
		WindDir: 0, U: 5, Stability: model.StabilityD,
		Sources: []model.PointSourceInput{
			{ID: "A", XY: model.XY{X: 0, Y: 0}, Q: q1, PhysicalH: 30},
			{ID: "B", XY: model.XY{X: 100, Y: 0}, Q: q2, PhysicalH: 60},
		},
		Receptors: []model.ReceptorInput{{ID: "R", XY: model.XY{X: 0, Y: -1000}}},
	}
}

// 多源作业提交后：主行 + 逐源明细都能完整回查，不只是一个总浓度数字。
func TestPostgresMultiSaveAndGet(t *testing.T) {
	pg := newStore(t)
	svc := multijob.NewService(pg)
	ctx := context.Background()

	req := multiReq(0.1, -0.3) // 带一个非法源，验证 partial 也能完整落库回查
	rec, err := svc.Submit(ctx, req, "it-multi-save-get")
	if err != nil {
		t.Fatalf("含非法源不应整批失败: %v", err)
	}
	if rec.Result.Status != "partial" {
		t.Fatalf("应 partial，got %s", rec.Result.Status)
	}
	got, err := svc.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("回查多源作业失败: %v", err)
	}
	// 每个源的原始输入（含坏源的负源强）都还原得出来。
	if len(got.Request.Sources) != 2 {
		t.Fatalf("应回查到 2 个源，got %d", len(got.Request.Sources))
	}
	if got.Request.Sources[0].Q != 0.1 || got.Request.Sources[1].Q != -0.3 {
		t.Errorf("源输入还原错误: %+v", got.Request.Sources)
	}
	if got.Request.Sources[0].ID != "A" || got.Request.Sources[1].ID != "B" {
		t.Errorf("源标识还原错误")
	}
	if got.Request.WindDir != 0 || got.Request.U != 5 ||
		string(got.Request.Stability) != "D" {
		t.Errorf("公共气象条件还原错误: %+v", got.Request)
	}
	cs := got.Result.Receptors[0].Contributions
	if len(cs) != 2 || cs[1].C != 0 || cs[1].Error == "" {
		t.Errorf("逐项贡献/零贡献原因回查不完整: %+v", cs)
	}
	if _, err := svc.Get(ctx, "missing-multi"); err == nil {
		t.Error("不存在的多源作业应返回错误")
	}
}

// 多源作业并发提交：主行+明细互不串写，且都能各自回查。
func TestPostgresMultiConcurrentSubmissions(t *testing.T) {
	pg := newStore(t)
	svc := multijob.NewService(pg)
	ctx := context.Background()
	const n = 30
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("it-multi-concurrent-%d", i)
			if _, err := svc.Submit(ctx, multiReq(0.001*float64(i+1), 0.002*float64(i+1)), id); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发提交多源作业出错: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("it-multi-concurrent-%d", i)
		rec, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatalf("回查 %s: %v", id, err)
		}
		if got := 0.001 * float64(i+1); rec.Request.Sources[0].Q != got {
			t.Errorf("%s 源强被串写: got %v want %v", id, rec.Request.Sources[0].Q, got)
		}
		if len(rec.Request.Sources) != 2 {
			t.Errorf("%s 源数量被串写", id)
		}
	}
}
