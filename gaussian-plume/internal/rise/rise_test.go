package rise

import (
	"math"
	"testing"

	"gaussian-plume/internal/model"
)

// 浮力通量用教材数值核对：
// Vs=20, Ds=3, Ts=400K, Ta=283K -> F = 9.80665*20*9*117/(4*400)
func TestBuoyancyFluxValue(t *testing.T) {
	f := BuoyancyFlux(20, 3, 400, 283)
	want := 9.80665 * 20 * 9 * 117 / (4 * 400)
	if math.Abs(f-want) > 1e-9 {
		t.Errorf("F=%v，期望 %v", f, want)
	}
}

// 风速翻倍时最终抬升应明显下降（约减半）。
func TestFinalRiseDecreasesWithWind(t *testing.T) {
	in := model.RiseInput{Vs: 20, Ds: 3, Ts: 400, Ta: 283, Stability: model.StabilityD}
	dh1, _, _, err := FinalRise(in)
	if err != nil {
		t.Fatal(err)
	}
	in.U = 4
	dh1u4, regime, f, err := FinalRise(in)
	if err != nil {
		t.Fatal(err)
	}
	in.U = 8
	dh2, _, _, err := FinalRise(in)
	if err != nil {
		t.Fatal(err)
	}
	if !(dh2 < dh1u4) {
		t.Error("风速增大抬升应下降")
	}
	if math.Abs(dh2/dh1u4-0.5) > 1e-9 {
		t.Errorf("风速翻倍抬升比=%v，期望 0.5", dh2/dh1u4)
	}
	// 本例 F≈129 >= 55，应走强浮力段。
	if f < buoyancyThreshold || regime != "neutral_buoyant_strong" {
		t.Errorf("F=%v regime=%s，期望强浮力段", f, regime)
	}
	if dh1 <= 0 {
		t.Error("抬升应为正")
	}
}

// 弱浮力（F<55）应走弱浮力分段。
func TestWeakBuoyancyRegime(t *testing.T) {
	in := model.RiseInput{Vs: 2, Ds: 0.5, Ts: 310, Ta: 300, U: 4, Stability: model.StabilityC}
	dh, regime, f, err := FinalRise(in)
	if err != nil {
		t.Fatal(err)
	}
	if f >= buoyancyThreshold {
		t.Errorf("F=%v 应 < 55", f)
	}
	if regime != "neutral_buoyant_weak" {
		t.Errorf("regime=%s 期望 weak", regime)
	}
	if dh <= 0 {
		t.Error("抬升应为正")
	}
}

// 有效源高 = 物理源高 + 抬升；有限距离按 1/3 律截断不超过最终值。
func TestEffectiveHeightAndTruncation(t *testing.T) {
	in := model.RiseInput{Vs: 20, Ds: 3, Ts: 400, Ta: 283, U: 4, Stability: model.StabilityD}
	hFinal, dhFinal, _, _, err := EffectiveHeight(80, in)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(hFinal-(80+dhFinal)) > 1e-9 {
		t.Errorf("H=%v 应等于 hs+ΔH=%v", hFinal, 80+dhFinal)
	}

	inX := in
	inX.X = 50 // 很近，1/3 律给出的抬升应小于最终抬升
	hNear, dhNear, _, _, err := EffectiveHeight(80, inX)
	if err != nil {
		t.Fatal(err)
	}
	if !(dhNear < dhFinal) {
		t.Errorf("近距离截断 ΔH=%v 应小于最终值 %v", dhNear, dhFinal)
	}
	if !(hNear < hFinal) {
		t.Error("近距离有效源高应更小")
	}
}

// 稳定层结给了递减率时应走出稳定公式（抬升为正、有限）。
func TestStableStratifiedRegime(t *testing.T) {
	in := model.RiseInput{
		Vs: 20, Ds: 3, Ts: 400, Ta: 283, U: 4,
		Stability: model.StabilityE, AmbientDTDz: 0.02,
	}
	dh, regime, _, err := FinalRise(in)
	if err != nil {
		t.Fatal(err)
	}
	if regime != "stable_stratified" {
		t.Errorf("regime=%s 期望 stable_stratified", regime)
	}
	if !(dh > 0 && !math.IsNaN(dh) && !math.IsInf(dh, 0)) {
		t.Errorf("稳定抬升应为正有限值，got %v", dh)
	}
}

func TestRiseRejectsBadInputs(t *testing.T) {
	cases := []model.RiseInput{
		{Ds: 0, Ts: 400, Ta: 283, U: 4},
		{Ds: 3, Ts: 200, Ta: 283, U: 4}, // Ts<=Ta
		{Ds: 3, Ts: 0, Ta: 283, U: 4},
		{Ds: 3, Ts: 400, Ta: 283, U: 0}, // 风速非正
	}
	for i, in := range cases {
		if _, _, _, _, err := EffectiveHeight(50, in); err == nil {
			t.Errorf("case %d 应报错", i)
		}
	}
}
