package multisource

import (
	"math"
	"testing"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/plume"
)

const cmpEps = 1e-12

func recAt(x, y float64) model.ReceptorInput {
	return model.ReceptorInput{ID: "R", XY: model.XY{X: x, Y: y}}
}

func groundSource(id string, x, y, q, h float64) model.PointSourceInput {
	return model.PointSourceInput{ID: id, XY: model.XY{X: x, Y: y}, Q: q, PhysicalH: h}
}

func baseReq(windDir, u float64, stab model.Stability,
	srcs []model.PointSourceInput, recs ...model.ReceptorInput) model.MultiSourceRequest {
	if recs == nil {
		recs = []model.ReceptorInput{recAt(0, -1000)}
	}
	return model.MultiSourceRequest{
		WindDir: windDir, U: u, Stability: stab,
		Sources: srcs, Receptors: recs,
	}
}

// 判据1：只放一个源、且它在受体连线的上风轴线上（风沿源→受体直吹），
// 多源接口结果必须与现有单源公式在数值上完全吻合。
func TestSingleSourceDegeneratesToSinglePlumeFormula(t *testing.T) {
	// 北风：源在 (0,0)，受体在 (0,-1000)，投影 (x=1000, y=0)。
	for _, stab := range []model.Stability{model.StabilityB, model.StabilityD, model.StabilityF} {
		for _, h := range []float64{0, 40, 120} {
			src := groundSource("S", 0, 0, 0.1, h)
			req := baseReq(0, 5, stab, []model.PointSourceInput{src})
			res := Run(req)
			if res.Status != "ok" {
				t.Fatalf("单源合法作业状态应为 ok，got %s", res.Status)
			}
			got := res.Receptors[0]
			if math.Abs(got.C-got.Contributions[0].C) > cmpEps {
				t.Errorf("单源时合成浓度必须等于该源贡献")
			}
			if math.Abs(got.Contributions[0].Share-1) > cmpEps {
				t.Errorf("单源占比应为 1，got %v", got.Contributions[0].Share)
			}
			single, err := plume.Calculate(model.PlumeParams{
				Q: 0.1, EffectiveH: h, U: 5, Stability: stab, X: 1000, Y: 0, Z: 0,
			})
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.C-single.C) > cmpEps*math.Max(1, single.C) {
				t.Errorf("stab=%s h=%v: 多源结果 %v 与单源公式 %v 不吻合", stab, h, got.C, single.C)
			}
			if math.Abs(*got.Contributions[0].SigmaY-single.SigmaY) > cmpEps ||
				math.Abs(*got.Contributions[0].SigmaZ-single.SigmaZ) > cmpEps {
				t.Errorf("扩散参数必须与单源公式一致")
			}
		}
	}

	// 带侧风偏移的单源也要精确吻合（投影 y≠0）：把受体放到北风下
	// (x=50, y=-1000)，应等价于单源公式 X=1000、Y=50。
	src := groundSource("S", 0, 0, 0.07, 30)
	req := baseReq(0, 4.2, model.StabilityC, []model.PointSourceInput{src}, recAt(50, -1000))
	res := Run(req)
	single, err := plume.Calculate(model.PlumeParams{
		Q: 0.07, EffectiveH: 30, U: 4.2, Stability: model.StabilityC, X: 1000, Y: -50, Z: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Receptors[0].C-single.C) > cmpEps*math.Max(1, single.C) {
		t.Errorf("带侧风偏移时多源 %v != 单源公式 %v", res.Receptors[0].C, single.C)
	}
}

// 判据2：两个源各自单独算出的浓度之和，必须等于两源一起提交的合成浓度。
// 任意位置、源高、稳定度组合都成立（稳态高斯解的线性叠加）。
func TestTwoSourceLinearSuperposition(t *testing.T) {
	cases := []struct {
		name       string
		stab       model.Stability
		windDir, u float64
		s1, s2     model.PointSourceInput
		rec        model.ReceptorInput
	}{
		{
			"同轴不同距离不同源高-D", model.StabilityD, 0, 5,
			groundSource("A", 0, 0, 0.1, 0),
			groundSource("B", 0, -400, 0.05, 60),
			recAt(0, -1000),
		},
		{
			"并排烟囱-B", model.StabilityB, 30, 6.3,
			groundSource("A", -120, 0, 0.2, 45),
			groundSource("B", 120, 0, 0.08, 80),
			recAt(30, -900),
		},
		{
			"斜风高源-F", model.StabilityF, 212.5, 2.1,
			groundSource("A", -300, 250, 0.03, 100),
			groundSource("B", 300, -250, 0.11, 10),
			recAt(0, 0),
		},
		{
			"一源上风侧零贡献-D", model.StabilityD, 0, 5,
			groundSource("A", 0, 0, 0.1, 30),
			groundSource("B", 0, -1200, 0.05, 30), // 在受体南方（下风方向更远处是相对受体…见下）
			recAt(0, -600),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			both := baseReq(tc.windDir, tc.u, tc.stab,
				[]model.PointSourceInput{tc.s1, tc.s2}, tc.rec)
			got := Run(both).Receptors[0].C

			c1 := Run(baseReq(tc.windDir, tc.u, tc.stab,
				[]model.PointSourceInput{tc.s1}, tc.rec)).Receptors[0].C
			c2 := Run(baseReq(tc.windDir, tc.u, tc.stab,
				[]model.PointSourceInput{tc.s2}, tc.rec)).Receptors[0].C

			if math.Abs(got-(c1+c2)) > cmpEps*math.Max(1, c1+c2) {
				t.Errorf("合成 %v != 单独贡献之和 %v+%v=%v", got, c1, c2, c1+c2)
			}
			// 逐项贡献也必须与各自单独提交完全一致，且占比和为 1。
			full := Run(both).Receptors[0]
			if math.Abs(full.Contributions[0].C-c1) > cmpEps*math.Max(1, c1) {
				t.Errorf("源1 批量内贡献 %v != 单独 %v", full.Contributions[0].C, c1)
			}
			if math.Abs(full.Contributions[1].C-c2) > cmpEps*math.Max(1, c2) {
				t.Errorf("源2 批量内贡献 %v != 单独 %v", full.Contributions[1].C, c2)
			}
			sumShare := full.Contributions[0].Share + full.Contributions[1].Share
			if c1+c2 > 0 && math.Abs(sumShare-1) > cmpEps {
				t.Errorf("占比之和应为 1，got %v", sumShare)
			}
		})
	}
}

// 判据3：同一个源挪到受体的正上风或正侧风，其单独贡献必须明显小于
// 把它放在受体正下风轴线上（源在受体上风方向）时；上风/侧风由投影自然压零，
// 没有任何方位特判。
func TestUpwindAndCrosswindContributionsSuppressed(t *testing.T) {
	const dist = 800.0
	// 北风：把受体钉在原点，源放在其不同方位，距离同为 800 m。
	downwindSrc := groundSource("D", 0, dist, 0.1, 50) // 源在北、受体在其正南方烟羽轴上
	upwindSrc := groundSource("U", 0, -dist, 0.1, 50)  // 源在南：受体在源上风
	crossSrc := groundSource("C", dist, 0, 0.1, 50)    // 源在东：受体在源正侧风
	rec := recAt(0, 0)

	cDown := Run(baseReq(0, 5, model.StabilityD, []model.PointSourceInput{downwindSrc}, rec)).Receptors[0].C
	cUp := Run(baseReq(0, 5, model.StabilityD, []model.PointSourceInput{upwindSrc}, rec)).Receptors[0].C
	cCross := Run(baseReq(0, 5, model.StabilityD, []model.PointSourceInput{crossSrc}, rec)).Receptors[0].C

	if cDown <= 0 {
		t.Fatal("下风轴线上的贡献应为正且显著")
	}
	if cUp != 0 {
		t.Errorf("正上风贡献必须按公式自然取 0，got %v", cUp)
	}
	if cCross != 0 {
		t.Errorf("正侧风（downwind=0）贡献必须取 0，got %v", cCross)
	}
	// “明显小于”：上风/侧风为零，下风轴上为正即严格压低；再补一个近侧风
	// 的有限距离点确认横向高斯衰减也在起作用。
	near := groundSource("N", dist, dist, 0.1, 50)
	cNear := Run(baseReq(0, 5, model.StabilityD, []model.PointSourceInput{near}, rec)).Receptors[0].C
	if !(cNear < cDown) {
		t.Errorf("带侧风偏移的贡献 %v 应小于同距离轴线贡献 %v", cNear, cDown)
	}

	// 上风源在批量结果里仍应逐项可见（贡献 0、占比 0、无 error），不能被丢掉。
	both := Run(baseReq(0, 5, model.StabilityD,
		[]model.PointSourceInput{downwindSrc, upwindSrc, crossSrc}, rec)).Receptors[0]
	if len(both.Contributions) != 3 {
		t.Fatalf("上风/侧风源不得从贡献清单里删除，got %d 项", len(both.Contributions))
	}
	if both.Contributions[1].C != 0 || both.Contributions[1].Error != "" {
		t.Errorf("上风源应为零贡献且无错误标记，got %+v", both.Contributions[1])
	}
	if math.Abs(both.C-cDown) > cmpEps*cDown {
		t.Errorf("零贡献源不得改变受体合成浓度")
	}
}

// 判据4：批量里一个源排放条件非法（源强为负/物理源高为负），只影响它自己
// （零贡献并说明原因），其余源照常叠加；多个受体点也互不牵连。
func TestBadSourceIsolatedZeroContribution(t *testing.T) {
	good := groundSource("G", 0, 0, 0.1, 30)
	badQ := groundSource("BQ", 0, -200, -0.5, 30)
	badH := groundSource("BH", 200, 0, 0.1, -8)
	req := baseReq(0, 5, model.StabilityD,
		[]model.PointSourceInput{good, badQ, badH},
		recAt(0, -1000), recAt(0, -500))

	res := Run(req)
	if res.Status != "partial" {
		t.Fatalf("含非法源应 partial，got %s", res.Status)
	}
	goodOnly := Run(baseReq(0, 5, model.StabilityD,
		[]model.PointSourceInput{good},
		recAt(0, -1000), recAt(0, -500)))

	for ri := range res.Receptors {
		got := res.Receptors[ri]
		cs := got.Contributions
		if len(cs) != 3 {
			t.Fatalf("受体%d 应仍有 3 项贡献", ri)
		}
		if cs[0].Error != "" || cs[0].C <= 0 {
			t.Errorf("受体%d 合法源应照常算出", ri)
		}
		if cs[1].C != 0 || cs[1].Error == "" {
			t.Errorf("受体%d 负源强源应零贡献带原因", ri)
		}
		if cs[2].C != 0 || cs[2].Error == "" {
			t.Errorf("受体%d 负源高源应零贡献带原因", ri)
		}
		want := goodOnly.Receptors[ri].C
		if math.Abs(got.C-want) > cmpEps*math.Max(1, want) {
			t.Errorf("受体%d 合成浓度 %v 应只等于合法源贡献 %v", ri, got.C, want)
		}
		if math.Abs(cs[0].Share-1) > cmpEps {
			t.Errorf("受体%d 合法源占比应为 1，got %v", ri, cs[0].Share)
		}
	}
}

// 非法热抬升参数同样只隔离该源；合法的带抬升源不受影响。
func TestBadBuoyancySourceIsolated(t *testing.T) {
	good := groundSource("G", 0, 0, 0.1, 50)
	good.BuoyancyRise = &model.RiseInput{Vs: 20, Ds: 3, Ts: 400, Ta: 283}
	bad := groundSource("B", 0, -200, 0.1, 50)
	bad.BuoyancyRise = &model.RiseInput{Vs: 20, Ds: -3, Ts: 400, Ta: 283} // ds 非法

	res := Run(baseReq(0, 5, model.StabilityD,
		[]model.PointSourceInput{good, bad}, recAt(0, -1000)))
	if res.Status != "partial" {
		t.Fatalf("应 partial，got %s", res.Status)
	}
	cs := res.Receptors[0].Contributions
	if cs[0].C <= 0 || cs[0].DeltaH == nil {
		t.Errorf("合法抬升源应照算并带 ΔH，got %+v", cs[0])
	}
	if cs[1].C != 0 || cs[1].Error == "" {
		t.Errorf("非法抬升源应零贡献带原因，got %+v", cs[1])
	}
}

// 全部源都不在下风半平面时，合成浓度为 0、占比不除零（全 0），不出 NaN。
func TestAllUpwindNoNaNShares(t *testing.T) {
	req := baseReq(0, 5, model.StabilityD,
		[]model.PointSourceInput{
			groundSource("A", 0, -100, 0.1, 30),
			groundSource("B", 100, -100, 0.2, 30),
		}, recAt(0, 0))
	res := Run(req)
	if res.Receptors[0].C != 0 {
		t.Errorf("全上风时合成浓度应为 0，got %v", res.Receptors[0].C)
	}
	for _, c := range res.Receptors[0].Contributions {
		if math.IsNaN(c.Share) || c.Share != 0 {
			t.Errorf("零合成时占比应为 0 而非 NaN，got %v", c.Share)
		}
	}
}
