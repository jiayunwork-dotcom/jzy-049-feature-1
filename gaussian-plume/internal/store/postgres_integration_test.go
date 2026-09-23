package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"gaussian-plume/internal/job"
	"gaussian-plume/internal/model"
	"gaussian-plume/internal/multisource"
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

func multiReq(q float64) model.MultiSourceRequest {
	return model.MultiSourceRequest{
		WindAngleDeg: 30, U: 5, Stability: model.StabilityD,
		Sources: []model.PointSource{
			{Name: "S1", X: 0, Y: 0, Q: q, PhysicalH: 60},
			{Name: "S2", X: 200, Y: 100, Q: 0.02, PhysicalH: 30},
		},
		Receptors: []model.ReceptorInput{{Name: "厂界", X: 1000, Y: 0}},
	}
}

// 多源作业在真实 PostgreSQL 上：提交后回查，每个源的输入与逐源拆解完整还原。
func TestPostgresMultiSourceSaveAndGet(t *testing.T) {
	pg := newStore(t)
	svc := multisource.NewService(pg)
	ctx := context.Background()

	rec, err := svc.Submit(ctx, multiReq(0.07), "it-ms-save-get")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if len(got.Request.Sources) != 2 || got.Request.Sources[0].Q != 0.07 ||
		got.Request.Sources[1].X != 200 || got.Request.WindAngleDeg != 30 {
		t.Errorf("回查源输入不完整: %+v", got.Request.Sources)
	}
	if len(got.Result.Receptors) != 1 ||
		len(got.Result.Receptors[0].Contributions) != 2 ||
		got.Result.Receptors[0].TotalC != rec.Result.Receptors[0].TotalC {
		t.Errorf("回查结果不完整: %+v", got.Result.Receptors)
	}
	var nf multisource.ErrNotFound
	if _, err := svc.Get(ctx, "missing-ms"); !errors.As(err, &nf) {
		t.Errorf("不存在的多源作业应返回 ErrNotFound, got %v", err)
	}
}

// 并发提交多条多源作业：各行不串、不互相覆盖，且都能各自回查。
func TestPostgresMultiSourceConcurrent(t *testing.T) {
	pg := newStore(t)
	svc := multisource.NewService(pg)
	ctx := context.Background()
	const n = 30
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("it-ms-concurrent-%d", i)
			if _, err := svc.Submit(ctx, multiReq(0.001*float64(i+1)), id); err != nil {
				errs <- err
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
		rec, err := svc.Get(ctx, fmt.Sprintf("it-ms-concurrent-%d", i))
		if err != nil {
			t.Fatalf("回查 %d: %v", i, err)
		}
		if want := 0.001 * float64(i+1); rec.Request.Sources[0].Q != want {
			t.Errorf("作业 %d 源强被串写: got %v want %v", i, rec.Request.Sources[0].Q, want)
		}
	}
}
