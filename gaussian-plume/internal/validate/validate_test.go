package validate

import (
	"strings"
	"testing"

	"gaussian-plume/internal/model"
)

func TestPointValidation(t *testing.T) {
	cases := []struct {
		pt     model.ScanPointInput
		wantOK bool
	}{
		{model.ScanPointInput{X: 1}, true},
		{model.ScanPointInput{X: 0}, false},
		{model.ScanPointInput{X: -3}, false},
		{model.ScanPointInput{X: 100, Z: -1}, false},
	}
	for i, c := range cases {
		msg := Point(c.pt)
		if c.wantOK != (msg == "") {
			t.Errorf("case %d: got %q", i, msg)
		}
	}
}

func TestSingleValidationStructuredErrors(t *testing.T) {
	// 同时给多个非法字段，应聚合为结构化 Errors 而不是硬算。
	req := SingleRequest{Q: -1, U: 0, Stability: "Z", X: -2, PhysicalH: -5}
	err := Single(req)
	if err == nil {
		t.Fatal("应报错")
	}
	es, ok := err.(Errors)
	if !ok {
		t.Fatalf("应返回结构化 Errors，got %T", err)
	}
	fields := map[string]bool{}
	for _, fe := range es {
		fields[fe.Field] = true
	}
	for _, f := range []string{"q", "u", "stability", "x", "physical_h"} {
		if !fields[f] {
			t.Errorf("缺少字段错误 %s（实际 %v）", f, es)
		}
	}
}

func TestSingleValid(t *testing.T) {
	req := SingleRequest{Q: 0.1, U: 5, Stability: "d", X: 100, PhysicalH: 0}
	if err := Single(req); err != nil {
		t.Fatalf("合法请求不应报错: %v", err)
	}
}

func TestScanRejectsEmptyGridAndConflict(t *testing.T) {
	// 空网格。
	bad := model.ScanRequest{Q: 0.1, U: 5, Stability: model.StabilityD}
	if err := Scan(&bad); err == nil || !strings.Contains(err.Error(), "points") {
		t.Errorf("空网格应报 points 错误，got %v", err)
	}
	// 抬升与给定有效源高冲突。
	conflict := model.ScanRequest{
		Q: 0.1, U: 5, Stability: model.StabilityD,
		Points:    []model.ScanPointInput{{X: 100}},
		UseGivenH: true, EffectiveH: 30,
		BuoyancyRise: &model.RiseInput{Vs: 10, Ds: 2, Ts: 400, Ta: 290},
	}
	if err := Scan(&conflict); err == nil || !strings.Contains(err.Error(), "effective_h") {
		t.Errorf("互斥冲突应报错，got %v", err)
	}
}

func TestScanNormalizesStabilityAndValidRise(t *testing.T) {
	req := model.ScanRequest{
		Q: 0.1, PhysicalH: 50, U: 5, Stability: "b",
		Points:       []model.ScanPointInput{{X: 100}},
		BuoyancyRise: &model.RiseInput{Vs: 10, Ds: 2, Ts: 400, Ta: 290},
	}
	if err := Scan(&req); err != nil {
		t.Fatalf("合法抬升请求不应报错: %v", err)
	}
	if req.Stability != model.StabilityB {
		t.Errorf("稳定度未归一化: %q", req.Stability)
	}
	if req.BuoyancyRise.U != 5 {
		t.Error("抬升风速缺省应回填作业风速")
	}
}
