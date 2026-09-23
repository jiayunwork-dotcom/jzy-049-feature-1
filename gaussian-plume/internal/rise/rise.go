// Package rise 实现 Briggs 热（浮力）烟羽抬升。
//
// 调用方要求叠加抬升时，有效源高 H = hs + ΔH，其中 ΔH 由本模块给出；
// 物理烟囱高度 hs 不得直接当作已抬升的有效源高。
//
// 钉死的 Briggs（1971/1975）公式：
//
//  1. 浮力通量（buoyancy flux，m^4/s^3）
//     F = g · Vs · Ds² · (Ts − Ta) / (4 · Ts)
//     （与 g·Vs·(Ds/2)²·(Ts−Ta)/Ts 等价的常用写法）
//
//  2. 中性/不稳定（A–D 或未提供层结）最终浮力抬升（m）
//     F < 55 :   ΔH = 21.425 · F^(3/4) / u        （小风/弱浮力，系数 21.425）
//     F ≥ 55 :   ΔH = 38.71  · F^(3/5) / u        （强浮力，系数 38.71）
//
//  3. 稳定（E–F）且给出环境温度递减率 -dTa/dz > 0 时，用稳定最终抬升
//     N² = (g/Ta) · (dT/dz + Γd)， Γd = 0.0098 K/m
//     其中 dT/dz = -AmbientDTDz（实际温度梯度，K/m）
//     ΔH = 2.6 · (F/(u·N²))^(1/3)
//     若层结参数缺失（AmbientDTDz<=0），稳定类回退到第 2 条中性式
//     （本服务钉死的兜底选择，文档明示）。
//
//  4. 有限下风距离（X>0）按 1/3 律截断到不超过最终抬升：
//     ΔH(x) = min( ΔH_final, 1.6·F^(1/3)·x^(2/3)/u )
//     X<=0 表示直接取最终抬升。
//
// 单位：g=9.80665 m/s²；温度 Ts、Ta 为 K；长度 m；速度 m/s。
// 本模块只处理热（浮力）抬升；纯动量抬升不在范围内。
package rise

import (
	"fmt"
	"math"

	"gaussian-plume/internal/model"
)

const (
	gravity      = 9.80665 // m/s²
	dryAdiabatic = 0.0098  // Γd，K/m
	// 浮力通量强弱分段阈值（m^4/s^3）。
	buoyancyThreshold = 55.0
	// 弱/强浮力最终抬升系数（钉死）。
	coefWeakBuoyancy   = 21.425
	coefStrongBuoyancy = 38.71
	// 稳定最终抬升系数与 1/3 律系数（钉死）。
	coefStableFinal = 2.6
	coefRiseGrowth  = 1.6
)

// BuoyancyFlux 计算浮力通量 F（m^4/s^3）。
func BuoyancyFlux(vs, ds, ts, ta float64) float64 {
	return gravity * vs * ds * ds * (ts - ta) / (4 * ts)
}

// FinalRise 计算 Briggs 最终抬升 ΔH（m）。返回 (ΔH, 使用的分段标签, F)。
func FinalRise(in model.RiseInput) (float64, string, float64, error) {
	if in.Ds <= 0 {
		return 0, "", 0, fmt.Errorf("烟囱内径 Ds=%g 必须为正", in.Ds)
	}
	if in.Ts <= 0 || in.Ta <= 0 {
		return 0, "", 0, fmt.Errorf("烟气温度 Ts=%g 与环境温度 Ta=%g 必须为正（K）", in.Ts, in.Ta)
	}
	if in.Ts <= in.Ta {
		return 0, "", 0, fmt.Errorf("热抬升需要烟气温度 Ts(%g)>环境温度 Ta(%g)", in.Ts, in.Ta)
	}
	f := BuoyancyFlux(in.Vs, in.Ds, in.Ts, in.Ta)
	if f <= 0 {
		return 0, "", 0, fmt.Errorf("浮力通量 F=%g 必须为正", f)
	}

	// 稳定层结公式：仅 E/F 且给出递减率时启用。
	// N² = (g/Ta)·(−dTa/dz − Γd)；输入 AmbientDTDz 即 −dTa/dz（K/m）。
	if (in.Stability == model.StabilityE || in.Stability == model.StabilityF) && in.AmbientDTDz > 0 {
		n2 := (gravity / in.Ta) * (in.AmbientDTDz - dryAdiabatic)
		if n2 > 0 {
			dh := coefStableFinal * math.Pow(f/(in.U*n2), 1.0/3.0)
			return dh, "stable_stratified", f, nil
		}
	}

	var dh float64
	regime := "neutral_buoyant_strong"
	if f < buoyancyThreshold {
		dh = coefWeakBuoyancy * math.Pow(f, 3.0/4.0) / in.U
		regime = "neutral_buoyant_weak"
	} else {
		dh = coefStrongBuoyancy * math.Pow(f, 3.0/5.0) / in.U
	}
	return dh, regime, f, nil
}

// EffectiveHeight 解析有效源高 H=hs+ΔH。
// hs 为物理烟囱高度（m）；X<=0 取最终抬升，否则按 1/3 律截断。
// 返回 (有效源高 H, 抬升量 ΔH, 浮力通量 F, 分段标签)。
func EffectiveHeight(hs float64, in model.RiseInput) (h, dh, f float64, regime string, err error) {
	if hs < 0 {
		return 0, 0, 0, "", fmt.Errorf("物理源高 hs=%g 不能为负", hs)
	}
	if !(in.U > 0) {
		return 0, 0, 0, "", fmt.Errorf("抬升计算需要风速 u=%g 为正", in.U)
	}
	final, regime, f, err := FinalRise(in)
	if err != nil {
		return 0, 0, 0, "", err
	}
	dh = final
	if in.X > 0 {
		growth := coefRiseGrowth * math.Pow(f, 1.0/3.0) * math.Pow(in.X, 2.0/3.0) / in.U
		dh = math.Min(final, growth)
	}
	return hs + dh, dh, f, regime, nil
}
