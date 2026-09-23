// Package plume 实现稳态高斯烟羽（连续点源）地面/任意受体高度浓度。
//
// 采用的定死公式（含地面全反射镜像项）：
//
//	C(x,y,z) = Q / (2π·σy·σz·u)
//	           · exp(−y²/(2σy²))
//	           · [ exp(−(z−H)²/(2σz²)) + exp(−(z+H)²/(2σz²)) ]
//	                        └── 直接项 ──┘   └── 地面全反射镜像项 ──┘
//
// 钉死的选择：前置系数用 2π；方括号保留镜像项。地面源（H=0）且受体在
// 源高（z=0）时，两项相等并成一块，括号 = 2·exp(0) = 2，得到解析极限：
//
//	C(x,0,0)|_{H=0} = Q / (π·σy·σz·u)
//
// 这是本模块必须满足的解析恒等式（测试锁死）。
package plume

import (
	"fmt"
	"math"

	"gaussian-plume/internal/dispersion"
	"gaussian-plume/internal/model"
)

// DirectAndImage 返回直接项与地面镜像项（方括号内两部分，未乘横向与前置因子）。
// 单独暴露是为了让镜像项这一物理分量可被直接核对。
func DirectAndImage(z, h, sigmaZ float64) (direct, image float64) {
	direct = math.Exp(-(z - h) * (z - h) / (2 * sigmaZ * sigmaZ))
	image = math.Exp(-(z + h) * (z + h) / (2 * sigmaZ * sigmaZ))
	return direct, image
}

// GroundCenterlineLimit 返回地面源、轴线、地面受体（H=0,y=0,z=0）的解析极限：
// Q/(π·σy·σz·u)。供调用方/测试与通用式交叉核对。
func GroundCenterlineLimit(q, sigmaY, sigmaZ, u float64) float64 {
	return q / (math.Pi * sigmaY * sigmaZ * u)
}

// Calculate 用高斯烟羽稳态解计算单个受体点浓度。
// σy、σz 由 Pasquill–Gifford 扩散参数模块按稳定度与下风向距离求出。
func Calculate(p model.PlumeParams) (model.PlumeResult, error) {
	sy, err := dispersion.SigmaY(p.Stability, p.X)
	if err != nil {
		return model.PlumeResult{}, err
	}
	sz, err := dispersion.SigmaZ(p.Stability, p.X)
	if err != nil {
		return model.PlumeResult{}, err
	}
	if !(p.U > 0) {
		return model.PlumeResult{}, fmt.Errorf("风速 u=%g 必须为正", p.U)
	}
	if p.Q < 0 {
		return model.PlumeResult{}, fmt.Errorf("源强 Q=%g 不能为负", p.Q)
	}
	if math.IsNaN(p.Y) || math.IsNaN(p.Z) || math.IsNaN(p.EffectiveH) {
		return model.PlumeResult{}, fmt.Errorf("位置/源高参数不能为 NaN")
	}

	direct, image := DirectAndImage(p.Z, p.EffectiveH, sz)
	crosswind := math.Exp(-(p.Y * p.Y) / (2 * sy * sy))

	c := p.Q / (2 * math.Pi * sy * sz * p.U) * crosswind * (direct + image)

	return model.PlumeResult{
		C:          c,
		SigmaY:     sy,
		SigmaZ:     sz,
		EffectiveH: p.EffectiveH,
	}, nil
}
