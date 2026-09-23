package dispersion

import (
	"math"
	"testing"

	"gaussian-plume/internal/model"
)

func classes() []model.Stability {
	return []model.Stability{
		model.StabilityA, model.StabilityB, model.StabilityC,
		model.StabilityD, model.StabilityE, model.StabilityF,
	}
}

// 同一稳定度下，σy、σz 必须随距离严格增大。
func TestSigmaGrowWithDistance(t *testing.T) {
	xs := []float64{1, 10, 50, 100, 200, 500, 999, 1000, 1001, 2000, 5000, 10000, 50000}
	for _, st := range classes() {
		var prevY, prevZ float64 = -1, -1
		for i, x := range xs {
			sy, err := SigmaY(st, x)
			if err != nil {
				t.Fatalf("SigmaY(%s,%v): %v", st, x, err)
			}
			sz, err := SigmaZ(st, x)
			if err != nil {
				t.Fatalf("SigmaZ(%s,%v): %v", st, x, err)
			}
			if i > 0 {
				if sy <= prevY {
					t.Errorf("%s: σy 在 x=%v 未随距离增大 (%v -> %v)", st, x, prevY, sy)
				}
				if sz <= prevZ {
					t.Errorf("%s: σz 在 x=%v 未随距离增大 (%v -> %v)", st, x, prevZ, sz)
				}
			}
			prevY, prevZ = sy, sz
		}
	}
}

// 同一距离处，σy、σz 必须随稳定度 A->F 严格变小。
func TestSigmaDecreaseWithStability(t *testing.T) {
	cls := classes()
	for _, x := range []float64{100, 500, 1000, 3000, 10000} {
		var prevY, prevZ float64 = math.Inf(1), math.Inf(1)
		for _, st := range cls {
			sy, _ := SigmaY(st, x)
			sz, _ := SigmaZ(st, x)
			if sy >= prevY {
				t.Errorf("x=%v: σy 在 %s 未随稳定度变小 (%v >= %v)", x, st, sy, prevY)
			}
			if sz >= prevZ {
				t.Errorf("x=%v: σz 在 %s 未随稳定度变小 (%v >= %v)", x, st, sz, prevZ)
			}
			prevY, prevZ = sy, sz
		}
	}
}

// 对距离的依赖必须是非线性幂律：近/远段指数都既不是 1 也不是 0。
func TestSigmaNonLinearPowerLaw(t *testing.T) {
	for _, st := range classes() {
		// 近段用相邻两点反解 σz、σy 指数，应明显不等于 1。
		s1, _ := SigmaZ(st, 100)
		s2, _ := SigmaZ(st, 200)
		eff := math.Log(s2/s1) / math.Log(2)
		if math.Abs(eff-1) < 0.02 {
			t.Errorf("%s: σz 近段指数 %v 过于接近一次正比", st, eff)
		}
		y1, _ := SigmaY(st, 100)
		y2, _ := SigmaY(st, 200)
		effY := math.Log(y2/y1) / math.Log(2)
		if math.Abs(effY-sigmaYNear[st].d) > 1e-9 {
			t.Errorf("%s: σy 近段指数 %v 与钉死值 %v 不符", st, effY, sigmaYNear[st].d)
		}
		if math.Abs(effY-1) < 1e-6 {
			t.Errorf("%s: σy 退化为一次正比", st)
		}
		// 远段（>1000）同样必须是非线性幂律。
		f1, _ := SigmaZ(st, 2000)
		f2, _ := SigmaZ(st, 4000)
		if math.Abs(math.Log(f2/f1)/math.Log(2)-sigmaZFarExp) > 1e-9 {
			t.Errorf("%s: σz 远段指数不符", st)
		}
	}
}

// σy、σz 分段必须在分界点 x=1000 连续。
func TestSigmaContinuousAtBreak(t *testing.T) {
	for _, st := range classes() {
		for name, near := range map[string]cd{"σy": sigmaYNear[st], "σz": sigmaZNear[st]} {
			atBreak := near.c * math.Pow(farBreakX, near.d)
			before, _ := SigmaY(st, farBreakX-1e-6)
			after, _ := SigmaY(st, farBreakX+1e-6)
			if name == "σz" {
				before, _ = SigmaZ(st, farBreakX-1e-6)
				after, _ = SigmaZ(st, farBreakX+1e-6)
			}
			if math.Abs(before-atBreak)/atBreak > 1e-3 {
				t.Errorf("%s %s: 分界点左侧不连续", name, st)
			}
			if math.Abs(after-atBreak)/atBreak > 1e-3 {
				t.Errorf("%s %s: 分界点右侧不连续", name, st)
			}
		}
	}
}

// 锚定到 P-G 规范值：σz 与 σy 在 100m / 1000m 与表内目标一致。
func TestSigmaAnchorValues(t *testing.T) {
	wantZ100 := map[model.Stability]float64{
		"A": 16.0, "B": 12.0, "C": 8.5, "D": 4.5, "E": 2.8, "F": 1.5,
	}
	wantZ1000 := map[model.Stability]float64{
		"A": 130.0, "B": 90.0, "C": 60.0, "D": 26.0, "E": 14.0, "F": 7.5,
	}
	wantY100 := map[model.Stability]float64{
		"A": 26.0, "B": 19.0, "C": 12.5, "D": 8.0, "E": 6.0, "F": 4.0,
	}
	wantY1000 := map[model.Stability]float64{
		"A": 170.0, "B": 130.0, "C": 90.0, "D": 70.0, "E": 45.0, "F": 28.0,
	}
	for _, st := range classes() {
		z100, _ := SigmaZ(st, 100)
		z1000, _ := SigmaZ(st, 1000)
		y100, _ := SigmaY(st, 100)
		y1000, _ := SigmaY(st, 1000)
		if math.Abs(z100-wantZ100[st])/wantZ100[st] > 0.02 {
			t.Errorf("%s: σz(100)=%v，期望锚定 %v", st, z100, wantZ100[st])
		}
		if math.Abs(z1000-wantZ1000[st])/wantZ1000[st] > 0.02 {
			t.Errorf("%s: σz(1000)=%v，期望锚定 %v", st, z1000, wantZ1000[st])
		}
		if math.Abs(y100-wantY100[st])/wantY100[st] > 0.02 {
			t.Errorf("%s: σy(100)=%v，期望锚定 %v", st, y100, wantY100[st])
		}
		if math.Abs(y1000-wantY1000[st])/wantY1000[st] > 0.02 {
			t.Errorf("%s: σy(1000)=%v，期望锚定 %v", st, y1000, wantY1000[st])
		}
	}
}

func TestSigmaRejectsBadInputs(t *testing.T) {
	if _, err := SigmaY(model.Stability("X"), 100); err == nil {
		t.Error("非法稳定度应报错")
	}
	if _, err := SigmaZ(model.Stability(""), 100); err == nil {
		t.Error("空稳定度应报错")
	}
	if _, err := SigmaY(model.StabilityD, 0); err == nil {
		t.Error("x=0 应报错")
	}
	if _, err := SigmaZ(model.StabilityD, -5); err == nil {
		t.Error("x<0 应报错")
	}
}
