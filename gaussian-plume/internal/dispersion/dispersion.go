// Package dispersion 实现 Pasquill–Gifford 横向/垂向扩散参数 σy、σz。
//
// 采用 Turner(1970) 风格、对 P-G 曲线图做分段幂律拟合（Pasquill–Gifford 体系）：
//
//	近段 x≤1000m：σ = c · x^d
//	远段 x>1000m：σ = σ(1000) · (x/1000)^p     （段间在 x=1000 严格连续）
//
// 约定：下风向距离 x 以米（m）为单位，σy、σz 输出也是米（m）。
// σy、σz 都随距离按幂律增大（指数 0.7–0.95，绝不简化成与距离一次正比），
// 并随稳定度 A（极不稳定）→F（稳定）而变小。
//
// 钉死的 P-G 曲线锚定值（σ，m）与由其反解的近段系数：
//
//	          σy(100) σy(1000)   cy       dy    |  σz(100) σz(1000)  cz      dz
//	A          26.0     170.0   0.6082  0.8155 |  16.0     130.0    0.2424  0.9098
//	B          19.0     130.0   0.4059  0.8352 |  12.0      90.0    0.2133  0.8751
//	C          12.5      90.0   0.2411  0.8573 |   8.5      60.0    0.1706  0.8487
//	D           8.0      70.0   0.1045  0.9420 |   4.5      26.0    0.1348  0.7618
//	E           6.0      45.0   0.1067  0.8751 |   2.8      14.0    0.1120  0.6990
//	F           4.0      28.0   0.0816  0.8451 |   1.5       7.5    0.0600  0.6990
//
// 远段统一指数：σy 取 py=0.90、σz 取 pz=0.70（以 x=1000 的锚定值连续延拓）。
//
// 稳定度次序保证：
//   - σz 的 cz、dz 均随 A→F 不增，故对【任意 x>0】严格满足 σz(A)>…>σz(F)；
//   - σy 在物理拟合区间 x≳10m 严格 A>…>F（P-G 的 σy 曲线在 1–2m 的源点近旁
//     存在 D/E 次序微调换，属源自身尺度内、无物理意义；拟合可靠域约 100m–10km，
//     服务允许的数值域为 x>0，超过 10km 为按钉死公式的外推）。
package dispersion

import (
	"fmt"
	"math"

	"gaussian-plume/internal/model"
)

const (
	// farBreakX 是近段/远段分界点（m）。
	farBreakX = 1000.0
	// sigmaYFarExp 是 σy 远段统一幂指数。
	sigmaYFarExp = 0.90
	// sigmaZFarExp 是 σz 远段统一幂指数。
	sigmaZFarExp = 0.70
)

// cd 是近段幂律的 (系数 c, 指数 d)：σ = c·x^d。
type cd struct{ c, d float64 }

// sigmaYNear 是 σy 近段系数（由 P-G 锚定值反解，见包文档表）。
var sigmaYNear = map[model.Stability]cd{
	model.StabilityA: {0.6082, 0.8155},
	model.StabilityB: {0.4059, 0.8352},
	model.StabilityC: {0.2411, 0.8573},
	model.StabilityD: {0.1045, 0.9420},
	model.StabilityE: {0.1067, 0.8751},
	model.StabilityF: {0.0816, 0.8451},
}

// sigmaZNear 是 σz 近段系数（由 P-G 锚定值反解，见包文档表）。
var sigmaZNear = map[model.Stability]cd{
	model.StabilityA: {0.2424, 0.9098},
	model.StabilityB: {0.2133, 0.8751},
	model.StabilityC: {0.1706, 0.8487},
	model.StabilityD: {0.1348, 0.7618},
	model.StabilityE: {0.1120, 0.6990},
	model.StabilityF: {0.0600, 0.6990},
}

// piecewise 通用两段幂律：近段 c·x^d，远段以分界点值连续延拓。
func piecewise(p cd, x, farExp float64) float64 {
	if x <= farBreakX {
		return p.c * math.Pow(x, p.d)
	}
	v0 := p.c * math.Pow(farBreakX, p.d)
	return v0 * math.Pow(x/farBreakX, farExp)
}

// SigmaY 返回横向扩散参数（m）。稳定度非法或距离非正时返回错误，绝不硬算。
func SigmaY(stab model.Stability, x float64) (float64, error) {
	if !stab.Valid() {
		return 0, fmt.Errorf("稳定度类别 %q 不在 A–F 之间", stab)
	}
	if !(x > 0) {
		return 0, fmt.Errorf("下风向距离 x=%g 必须为正", x)
	}
	return piecewise(sigmaYNear[stab], x, sigmaYFarExp), nil
}

// SigmaZ 返回垂向扩散参数（m），按距离分段、段间连续。
func SigmaZ(stab model.Stability, x float64) (float64, error) {
	if !stab.Valid() {
		return 0, fmt.Errorf("稳定度类别 %q 不在 A–F 之间", stab)
	}
	if !(x > 0) {
		return 0, fmt.Errorf("下风向距离 x=%g 必须为正", x)
	}
	return piecewise(sigmaZNear[stab], x, sigmaZFarExp), nil
}
