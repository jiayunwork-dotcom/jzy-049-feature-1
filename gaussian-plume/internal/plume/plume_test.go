package plume

import (
	"math"
	"testing"

	"gaussian-plume/internal/dispersion"
	"gaussian-plume/internal/model"
)

const eps = 1e-12

func baseParams(q, u float64, stab model.Stability, h, x float64) model.PlumeParams {
	return model.PlumeParams{
		Q: q, EffectiveH: h, U: u, Stability: stab,
		X: x, Y: 0, Z: 0,
	}
}

// 判据1：源强翻倍、其余不变，所有受体点浓度按比例翻倍。
func TestConcentrationLinearInQ(t *testing.T) {
	for _, x := range []float64{100, 500, 1000, 3000, 8000} {
		p1 := baseParams(0.1, 5, model.StabilityD, 60, x)
		p2 := baseParams(0.2, 5, model.StabilityD, 60, x)
		r1, err := Calculate(p1)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := Calculate(p2)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(r2.C/r1.C-2) > 1e-9 {
			t.Errorf("x=%v: Q 翻倍后浓度比=%v，期望 2", x, r2.C/r1.C)
		}
	}
}

// 判据2：风速翻倍（σ 不显式依赖风速），轴线地面浓度明显下降（近于减半）。
func TestConcentrationDecreasesWithWind(t *testing.T) {
	for _, x := range []float64{200, 1000, 5000} {
		lo, err := Calculate(baseParams(0.1, 3, model.StabilityD, 50, x))
		if err != nil {
			t.Fatal(err)
		}
		hi, err := Calculate(baseParams(0.1, 6, model.StabilityD, 50, x))
		if err != nil {
			t.Fatal(err)
		}
		// 抬升不随本次接口耦合，σ 相同，故精确减半。
		if math.Abs(hi.C/lo.C-0.5) > 1e-9 {
			t.Errorf("x=%v: 风速翻倍后浓度比=%v，期望 0.5", x, hi.C/lo.C)
		}
		if !(hi.C < lo.C) {
			t.Errorf("x=%v: 风速增大浓度未下降", x)
		}
	}
}

// 判据3：稳定度从极不稳定 A 改到稳定 F，同距离 σz 变小；
// 地面轴线浓度沿程分布形态随之改变（最大落点位置/归一化曲线不同）。
func TestStabilityChangesSigmaZAndProfile(t *testing.T) {
	x := 1000.0
	szA, _ := dispersion.SigmaZ(model.StabilityA, x)
	szF, _ := dispersion.SigmaZ(model.StabilityF, x)
	if !(szF < szA) {
		t.Fatalf("σz(F)=%v 应小于 σz(A)=%v", szF, szA)
	}

	xs := []float64{100, 200, 400, 800, 1500, 3000, 6000, 10000}
	prof := func(st model.Stability) []float64 {
		cs := make([]float64, len(xs))
		maxC, idx := -1.0, 0
		for i, xx := range xs {
			r, err := Calculate(baseParams(0.1, 5, st, 50, xx))
			if err != nil {
				t.Fatal(err)
			}
			cs[i] = r.C
			if r.C > maxC {
				maxC, idx = r.C, i
			}
		}
		_ = idx
		return cs
	}
	pA := prof(model.StabilityA)
	pF := prof(model.StabilityF)

	// 归一化后的沿程曲线形态必须不同：比较若干固定点的相对浓度。
	shape := func(p []float64) []float64 {
		mx := 0.0
		for _, v := range p {
			if v > mx {
				mx = v
			}
		}
		out := make([]float64, len(p))
		for i, v := range p {
			out[i] = v / mx
		}
		return out
	}
	sA, sF := shape(pA), shape(pF)
	maxDiff := 0.0
	for i := range sA {
		if d := math.Abs(sA[i] - sF[i]); d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff < 0.05 {
		t.Errorf("A 与 F 的归一化沿程形态几乎不变（maxDiff=%v），不符合预期", maxDiff)
	}
}

// 判据4：地面源 H=0、地面受体 z=0、轴线 y=0 时，
// 镜像项与直接项合并，通用式必须等于解析极限 Q/(πσyσzu)。
func TestGroundSourceImageMergesToAnalyticLimit(t *testing.T) {
	for _, x := range []float64{50, 100, 1000, 5000} {
		p := baseParams(0.1, 5, model.StabilityD, 0, x) // H=0
		r, err := Calculate(p)
		if err != nil {
			t.Fatal(err)
		}
		want := GroundCenterlineLimit(p.Q, r.SigmaY, r.SigmaZ, p.U)
		if math.Abs(r.C-want) > eps*want {
			t.Errorf("x=%v: 地面源轴线浓度 %v != 解析极限 %v", x, r.C, want)
		}
		// 直接项与镜像项在 z=H=0 处都应等于 1，合并为 2。
		d, img := DirectAndImage(0, 0, r.SigmaZ)
		if math.Abs(d-1) > eps || math.Abs(img-1) > eps {
			t.Errorf("地面源两项应各为1，got %v,%v", d, img)
		}
		// 镜像项不可丢：保留镜像项的结果应恰为只算直接项的 2 倍。
		onlyDirect := p.Q / (2 * math.Pi * r.SigmaY * r.SigmaZ * p.U) * d
		if math.Abs(r.C-2*onlyDirect) > eps*r.C {
			t.Errorf("地面源处镜像项必须使浓度翻倍")
		}
	}
}

// 有限源高的非轴线点：横向偏移应降低浓度；镜像项仍为正贡献。
func TestCrosswindAndImageContribution(t *testing.T) {
	r0, _ := Calculate(baseParams(0.1, 5, model.StabilityD, 40, 1000))
	py := baseParams(0.1, 5, model.StabilityD, 40, 1000)
	py.Y = 100
	ry, _ := Calculate(py)
	if !(ry.C < r0.C) {
		t.Error("横向偏移后浓度应降低")
	}
	// 有效源高越高、近处地面浓度越低（镜像项使该结论在足够近处成立）。
	low, _ := Calculate(baseParams(0.1, 5, model.StabilityD, 10, 200))
	high, _ := Calculate(baseParams(0.1, 5, model.StabilityD, 120, 200))
	if !(high.C < low.C) {
		t.Error("近距离处提高源高应降低地面浓度")
	}
}

func TestCalculateRejectsBadInputs(t *testing.T) {
	if _, err := Calculate(baseParams(-1, 5, model.StabilityD, 0, 100)); err == nil {
		t.Error("负源强应报错")
	}
	if _, err := Calculate(baseParams(1, 0, model.StabilityD, 0, 100)); err == nil {
		t.Error("零风速应报错")
	}
	if _, err := Calculate(baseParams(1, 5, model.Stability("G"), 0, 100)); err == nil {
		t.Error("非法稳定度应报错")
	}
	if _, err := Calculate(baseParams(1, 5, model.StabilityD, 0, 0)); err == nil {
		t.Error("非正距离应报错")
	}
}
