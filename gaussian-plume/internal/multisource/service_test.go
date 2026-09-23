package multisource

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/plume"
	"gaussian-plume/internal/rise"
)

// legacyC 走既有单源公式（plume.Calculate）算地面轴线浓度，作为对照基准。
func legacyC(t *testing.T, q, h, u float64, stab model.Stability, x, y float64) float64 {
	t.Helper()
	res, err := plume.Calculate(model.PlumeParams{
		Q: q, EffectiveH: h, U: u, Stability: stab, X: x, Y: y, Z: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.C
}

func runOne(t *testing.T, req model.MultiSourceRequest) model.MultiSourceResult {
	t.Helper()
	svc := NewService(NewMemoryStore())
	if err := ValidateJob(&req); err != nil {
		t.Fatalf("测试用例本身应通过作业级校验: %v", err)
	}
	return svc.Run(req)
}

// 判据一（钉死）：只放一个源、且它落在受体连线的上风轴线上时，
// 多源接口的结果要与现有单源公式在数值上完全吻合（逐位相等）。
func TestSingleSourceMatchesLegacyFormulaExactly(t *testing.T) {
	// 轴对齐：θ=0°，源在原点，受体在正东 1000m —— 投影精确落在下风轴线上。
	req := model.MultiSourceRequest{
		WindAngleDeg: 0, U: 5, Stability: model.StabilityD,
		Sources:   []model.PointSource{{X: 0, Y: 0, Q: 0.1, PhysicalH: 60}},
		Receptors: []model.ReceptorInput{{X: 1000, Y: 0}},
	}
	res := runOne(t, req)
	got := res.Receptors[0].TotalC
	want := legacyC(t, 0.1, 60, 5, model.StabilityD, 1000, 0)
	if got != want {
		t.Errorf("单源退化应与老公式逐位一致: got %v, want %v", got, want)
	}
	c := res.Receptors[0].Contributions[0]
	if c.Status != StatusOK || c.DownwindX != 1000 || c.CrosswindY != 0 || c.Share != 1 {
		t.Errorf("单源贡献拆解异常: %+v", c)
	}

	// 旋转 30°：源不在原点、受体在源的下风轴线上，投影后 x'=800。
	theta := 30 * math.Pi / 180
	dx, dy := 800*math.Cos(theta), 800*math.Sin(theta)
	req = model.MultiSourceRequest{
		WindAngleDeg: 30, U: 4, Stability: model.StabilityE,
		Sources:   []model.PointSource{{X: 200, Y: 300, Q: 0.05, PhysicalH: 40}},
		Receptors: []model.ReceptorInput{{X: 200 + dx, Y: 300 + dy}},
	}
	res = runOne(t, req)
	got = res.Receptors[0].TotalC
	want = legacyC(t, 0.05, 40, 4, model.StabilityE, 800, 0)
	// 投影经 cos/sin，允许末位舍入差异，但相对偏差须小于 1e-9。
	if math.Abs(got-want)/want > 1e-9 {
		t.Errorf("旋转坐标系下单源退化与老公式不符: got %v, want %v", got, want)
	}
}

// 判据一（挂抬升）：单源带 Briggs 热抬升时，多源接口与"手工逐源解析"完全一致。
func TestSingleSourceWithRiseMatchesLegacy(t *testing.T) {
	riseIn := model.RiseInput{Vs: 20, Ds: 3, Ts: 400, Ta: 283}
	req := model.MultiSourceRequest{
		WindAngleDeg: 0, U: 5, Stability: model.StabilityD,
		Sources:   []model.PointSource{{X: 0, Y: 0, Q: 0.1, PhysicalH: 80, BuoyancyRise: &riseIn}},
		Receptors: []model.ReceptorInput{{X: 500, Y: 0}},
	}
	res := runOne(t, req)
	got := res.Receptors[0].TotalC

	// 手工基准：按受体下风距离截断抬升，再走老公式。
	manualIn := riseIn
	manualIn.Stability = model.StabilityD
	manualIn.U = 5
	manualIn.X = 500
	h, _, _, _, err := rise.EffectiveHeight(80, manualIn)
	if err != nil {
		t.Fatal(err)
	}
	want := legacyC(t, 0.1, h, 5, model.StabilityD, 500, 0)
	if got != want {
		t.Errorf("带抬升单源退化应逐位一致: got %v, want %v", got, want)
	}
	if res.Receptors[0].Contributions[0].EffectiveH != h {
		t.Errorf("有效源高应一致: got %v, want %v",
			res.Receptors[0].Contributions[0].EffectiveH, h)
	}
}

// 判据二（钉死）：两个源各自单独算出的地面浓度，加起来正好等于
// 一起提交时的合成浓度 —— 线性叠加，与位置/源高/稳定度组合无关。
func TestTwoSourceLinearSuperposition(t *testing.T) {
	riseIn := model.RiseInput{Vs: 15, Ds: 2, Ts: 380, Ta: 283}
	srcA := model.PointSource{Name: "A", X: 0, Y: 0, Q: 0.10, PhysicalH: 60}
	srcB := model.PointSource{Name: "B", X: 120, Y: 80, Q: 0.03, PhysicalH: 45, BuoyancyRise: &riseIn}
	receptors := []model.ReceptorInput{{X: 1000, Y: 0}, {X: 600, Y: -150}, {X: 2000, Y: 300}}

	for _, stab := range []model.Stability{model.StabilityA, model.StabilityD, model.StabilityF} {
		for _, wind := range []float64{0, 45, 210} {
			base := model.MultiSourceRequest{
				WindAngleDeg: wind, U: 4, Stability: stab, Receptors: receptors,
			}
			onlyA := base
			onlyA.Sources = []model.PointSource{srcA}
			onlyB := base
			onlyB.Sources = []model.PointSource{srcB}
			both := base
			both.Sources = []model.PointSource{srcA, srcB}

			resA, resB, resBoth := runOne(t, onlyA), runOne(t, onlyB), runOne(t, both)
			for i := range receptors {
				sum := resA.Receptors[i].TotalC + resB.Receptors[i].TotalC
				got := resBoth.Receptors[i].TotalC
				if got != sum {
					t.Errorf("stab=%s wind=%v 受体%d: 合成 %v != 单源和 %v",
						stab, wind, i, got, sum)
				}
				// 逐源拆解也必须等于各自单跑的结果。
				cA := resBoth.Receptors[i].Contributions[0].C
				cB := resBoth.Receptors[i].Contributions[1].C
				if cA != resA.Receptors[i].TotalC || cB != resB.Receptors[i].TotalC {
					t.Errorf("stab=%s wind=%v 受体%d: 逐源拆解 (%v,%v) 与单跑 (%v,%v) 不符",
						stab, wind, i, cA, cB, resA.Receptors[i].TotalC, resB.Receptors[i].TotalC)
				}
			}
		}
	}
}

// 判据三（钉死）：源挪到受体正上风或正侧风时，其贡献要明显小于正下风轴线的情形。
func TestUpwindCrosswindSuppressed(t *testing.T) {
	src := model.PointSource{X: 0, Y: 0, Q: 0.1, PhysicalH: 60}
	rec := []model.ReceptorInput{{X: 1000, Y: 0}}
	mk := func(wind float64) model.MultiSourceRequest {
		return model.MultiSourceRequest{
			WindAngleDeg: wind, U: 5, Stability: model.StabilityD,
			Sources: []model.PointSource{src}, Receptors: rec,
		}
	}
	down := runOne(t, mk(0)).Receptors[0].Contributions[0]  // 受体在源正下风轴线
	up := runOne(t, mk(180)).Receptors[0].Contributions[0]  // 受体在源正上风
	side := runOne(t, mk(90)).Receptors[0].Contributions[0] // 受体在源正侧风
	near := runOne(t, mk(10)).Receptors[0].Contributions[0] // 偏离轴线 10°

	if down.C <= 0 || down.Status != StatusOK {
		t.Fatalf("正下风应有正常贡献: %+v", down)
	}
	if up.C != 0 || up.Status != StatusZero {
		t.Errorf("正上风贡献应为零: %+v", up)
	}
	// 正侧风：cos(90°) 的浮点舍入使 x' 落在 ±1e-13 边界上，状态可能记 ok
	// 也可能记 zero，但两种路径下公式都把浓度压到精确的 0。
	if side.C != 0 {
		t.Errorf("正侧风贡献应为零: %+v", side)
	}
	// 偏离轴线 10°：公式自然压低（高斯侧风项），远小于轴线上的值。
	if !(near.C > 0) || near.C >= down.C/10 {
		t.Errorf("近侧风贡献 %v 应远小于正下风 %v", near.C, down.C)
	}
}

// 判据四（钉死）：批量里单个源排放条件非法，只影响它自己的贡献
// （零贡献 + 原因），其余源照常叠加，整批不报错。
func TestInvalidSourceIsolated(t *testing.T) {
	good1 := model.PointSource{Name: "good1", X: 0, Y: 0, Q: 0.10, PhysicalH: 60}
	badQ := model.PointSource{Name: "badQ", X: 50, Y: 0, Q: -1, PhysicalH: 60}
	badH := model.PointSource{Name: "badH", X: 80, Y: 0, Q: 0.05, PhysicalH: -10}
	good2 := model.PointSource{Name: "good2", X: 200, Y: 100, Q: 0.02, PhysicalH: 30}
	recs := []model.ReceptorInput{{X: 1000, Y: 0}, {X: 1500, Y: 200}}

	mk := func(sources ...model.PointSource) model.MultiSourceRequest {
		return model.MultiSourceRequest{
			WindAngleDeg: 0, U: 5, Stability: model.StabilityD,
			Sources: sources, Receptors: recs,
		}
	}
	resGood := runOne(t, mk(good1, good2))
	resMix := runOne(t, mk(good1, badQ, badH, good2))

	if resMix.Status != "partial" {
		t.Errorf("含非法源应 partial，got %s", resMix.Status)
	}
	for i := range recs {
		rr := resMix.Receptors[i]
		if rr.Status != "partial" {
			t.Errorf("受体%d 应 partial，got %s", i, rr.Status)
		}
		// 合成浓度 = 两个好源之和，与不含坏源时逐位一致。
		want := resGood.Receptors[i].TotalC
		if rr.TotalC != want {
			t.Errorf("受体%d: 含坏源合成 %v != 纯好源合成 %v", i, rr.TotalC, want)
		}
		if len(rr.Contributions) != 4 {
			t.Fatalf("受体%d 应有 4 条贡献拆解，got %d", i, len(rr.Contributions))
		}
		// 坏源：零贡献、占比如零、带原因；好源：与纯好源时逐位一致。
		for _, idx := range []int{1, 2} {
			c := rr.Contributions[idx]
			if c.Status != StatusInvalid || c.C != 0 || c.Share != 0 || c.Reason == "" {
				t.Errorf("受体%d 坏源%d 拆解异常: %+v", i, idx, c)
			}
		}
		if rr.Contributions[0].C != resGood.Receptors[i].Contributions[0].C ||
			rr.Contributions[3].C != resGood.Receptors[i].Contributions[1].C {
			t.Errorf("受体%d 好源贡献被坏源牵连", i)
		}
	}
}

// 占比拆解：合成非零时各源占比和为 1；全部零贡献时占比全 0（不除零）。
func TestShareDecomposition(t *testing.T) {
	req := model.MultiSourceRequest{
		WindAngleDeg: 0, U: 5, Stability: model.StabilityD,
		Sources: []model.PointSource{
			{X: 0, Y: 0, Q: 0.1, PhysicalH: 60},
			{X: 0, Y: 200, Q: 0.1, PhysicalH: 60},
		},
		Receptors: []model.ReceptorInput{{X: 1000, Y: 0}},
	}
	rr := runOne(t, req).Receptors[0]
	sum := 0.0
	for _, c := range rr.Contributions {
		sum += c.Share
		if c.Share < 0 || c.Share > 1 {
			t.Errorf("占比越界: %+v", c)
		}
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Errorf("占比和应为 1，got %v", sum)
	}
	// 主导源可定位：轴线上的源占比应显著大于侧风 200m 的源。
	if !(rr.Contributions[0].Share > rr.Contributions[1].Share) {
		t.Errorf("轴线源占比应更大: %+v", rr.Contributions)
	}

	// 全部源都在上风 → 合成 0，占比全 0。
	req.WindAngleDeg = 180
	rr = runOne(t, req).Receptors[0]
	if rr.TotalC != 0 {
		t.Errorf("全上风合成应为 0，got %v", rr.TotalC)
	}
	for _, c := range rr.Contributions {
		if c.Share != 0 {
			t.Errorf("零合成时占比应为 0: %+v", c)
		}
	}
}

// 单个受体坐标非法只影响该受体条目，其余受体照常出结果。
func TestInvalidReceptorIsolated(t *testing.T) {
	req := model.MultiSourceRequest{
		WindAngleDeg: 0, U: 5, Stability: model.StabilityD,
		Sources: []model.PointSource{{X: 0, Y: 0, Q: 0.1, PhysicalH: 60}},
		Receptors: []model.ReceptorInput{
			{X: 1000, Y: 0},
			{X: math.NaN(), Y: 0},
			{X: 2000, Y: 0},
		},
	}
	res := runOne(t, req)
	if res.Status != "partial" {
		t.Errorf("应 partial，got %s", res.Status)
	}
	if res.Receptors[0].TotalC <= 0 || res.Receptors[2].TotalC <= 0 {
		t.Error("合法受体应照常出浓度")
	}
	bad := res.Receptors[1]
	if bad.Status != "invalid" || bad.Error == "" || bad.TotalC != 0 {
		t.Errorf("非法受体条目异常: %+v", bad)
	}
}

// 作业级非法（风向角非有限、风速非正、稳定度非法、源批/受体批为空）整体拒绝，不落库。
func TestSubmitRejectsJobLevelInvalid(t *testing.T) {
	valid := model.MultiSourceRequest{
		WindAngleDeg: 0, U: 5, Stability: model.StabilityD,
		Sources:   []model.PointSource{{X: 0, Y: 0, Q: 0.1, PhysicalH: 60}},
		Receptors: []model.ReceptorInput{{X: 1000, Y: 0}},
	}
	cases := map[string]func(*model.MultiSourceRequest){
		"风向角NaN": func(r *model.MultiSourceRequest) { r.WindAngleDeg = math.NaN() },
		"风速为零":   func(r *model.MultiSourceRequest) { r.U = 0 },
		"稳定度非法":  func(r *model.MultiSourceRequest) { r.Stability = "Z" },
		"源批为空":   func(r *model.MultiSourceRequest) { r.Sources = nil },
		"受体批为空":  func(r *model.MultiSourceRequest) { r.Receptors = nil },
	}
	for name, mutate := range cases {
		store := NewMemoryStore()
		svc := NewService(store)
		req := valid
		mutate(&req)
		if _, err := svc.Submit(context.Background(), req, "bad-"+name); err == nil {
			t.Errorf("%s 应整体拒绝", name)
		}
		if store.Len() != 0 {
			t.Errorf("%s 不应落库", name)
		}
	}
}

// 提交后可回查：每个源的输入、每个受体的逐源拆解都能完整还原。
func TestSubmitAndGetRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	req := model.MultiSourceRequest{
		WindAngleDeg: 45, U: 4, Stability: model.StabilityE,
		Sources: []model.PointSource{
			{Name: "S1", X: 0, Y: 0, Q: 0.1, PhysicalH: 60},
			{Name: "S2", X: 300, Y: -50, Q: 0.04, PhysicalH: 25},
		},
		Receptors: []model.ReceptorInput{{Name: "厂界东", X: 1000, Y: 0}},
	}
	rec, err := svc.Submit(ctx, req, "ms-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, "ms-1")
	if err != nil {
		t.Fatal(err)
	}
	// 输入还原：每个源的坐标与排放条件都在。
	if len(got.Request.Sources) != 2 ||
		got.Request.Sources[1].Name != "S2" ||
		got.Request.Sources[1].X != 300 || got.Request.Sources[1].Y != -50 ||
		got.Request.Sources[1].Q != 0.04 || got.Request.Sources[1].PhysicalH != 25 {
		t.Errorf("回查源输入不完整: %+v", got.Request.Sources)
	}
	// 结果还原：合成浓度与逐源拆解和提交时一致。
	if got.Result.Receptors[0].TotalC != rec.Result.Receptors[0].TotalC ||
		len(got.Result.Receptors[0].Contributions) != 2 {
		t.Errorf("回查结果不一致: %+v", got.Result.Receptors[0])
	}
	if _, err := svc.Get(ctx, "missing"); err == nil {
		t.Error("不存在的作业应报错")
	}
}

// 并发提交多条多源作业：各自结果不串、不互相覆盖，且可分别回查。
func TestConcurrentSubmissionsDoNotInterleave(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := model.MultiSourceRequest{
				WindAngleDeg: float64(i * 7), U: 5, Stability: model.StabilityD,
				Sources: []model.PointSource{
					{X: 0, Y: 0, Q: 0.01 * float64(i+1), PhysicalH: 60},
					{X: 100, Y: 50, Q: 0.02, PhysicalH: 30},
				},
				Receptors: []model.ReceptorInput{{X: 1000, Y: 0}},
			}
			if _, err := svc.Submit(ctx, req, fmt.Sprintf("ms-%d", i)); err != nil {
				t.Errorf("submit %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if store.Len() != n {
		t.Fatalf("应落库 %d 条，got %d", n, store.Len())
	}
	for i := 0; i < n; i++ {
		rec, err := svc.Get(ctx, fmt.Sprintf("ms-%d", i))
		if err != nil {
			t.Fatalf("回查 ms-%d: %v", i, err)
		}
		wantQ := 0.01 * float64(i+1)
		if rec.Request.Sources[0].Q != wantQ || rec.Request.WindAngleDeg != float64(i*7) {
			t.Errorf("ms-%d 输入被串写: %+v", i, rec.Request)
		}
		// 结果必须与自己的输入自洽：用回查到的输入重算，合成浓度逐位一致。
		again := svc.Run(rec.Request)
		if again.Receptors[0].TotalC != rec.Result.Receptors[0].TotalC {
			t.Errorf("ms-%d 结果被串写: %v != 重算 %v",
				i, rec.Result.Receptors[0].TotalC, again.Receptors[0].TotalC)
		}
	}
}
