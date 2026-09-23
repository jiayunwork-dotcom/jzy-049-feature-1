// Package projection 提供多源合成所需的共享平面坐标系投影骨架。
//
// 背景：单源公式只用一个下风距离 x 描述"源→受体"关系，隐含风恰好吹在
// 源到受体的连线上。多源场景下各源到同一受体的方向与风向一般不重合，
// 必须先把源与受体摆进同一个平面坐标系，再按统一风向把每个"源→受体"
// 位移投影成沿风下风分量 x' 与垂直风向的侧风分量 y'，才能套用烟羽公式。
//
// 钉死的约定（全服务唯一，所有源与受体统一套用，不得另起第二套）：
//
//   - 平面坐标系：x 轴朝东、y 轴朝北（数学右手系），原点任意但一次作业内一致；
//   - 风向角 wind_angle_deg（度）：风**吹向**（去向）的方位角，自 +x 轴起算、
//     逆时针为正。即 0°=吹向正东，90°=吹向正北。风的来向方位角 = θ+180°；
//   - 投影：位移 d = (dx, dy) = 受体 − 源，
//     x' =  dx·cosθ + dy·sinθ        （沿风下风分量）
//     y' = −dx·sinθ + dy·cosθ        （侧风分量，+y' 在风向左侧）
//
// 本包只做纯几何投影，不含任何浓度计算；浓度公式、扩散参数、热抬升
// 的数学在 plume/dispersion/rise 包，原样复用、一字不改。
package projection

import (
	"fmt"
	"math"
)

// WindVector 返回风向角 θ（度，数学约定）对应的单位风向向量 (cosθ, sinθ)。
func WindVector(thetaDeg float64) (ux, uy float64) {
	r := thetaDeg * math.Pi / 180
	return math.Cos(r), math.Sin(r)
}

// NormalizeDeg 把角度归一化到 [0, 360)。
func NormalizeDeg(thetaDeg float64) float64 {
	return math.Mod(math.Mod(thetaDeg, 360)+360, 360)
}

// Project 把"源→受体"位移 (dx, dy) 投影到风坐标系，返回下风分量 x' 与
// 侧风分量 y'（+y' 在风向左侧）。θ 为风吹向方位角（度，数学约定）。
//
// 物理读法：x'>0 表示受体在源的下风方向；x'≤0 表示受体不在该源的
// 下风半平面（正侧风或上风），稳态烟羽解在该半平面的自然延拓为零。
func Project(dx, dy, thetaDeg float64) (x, y float64) {
	ux, uy := WindVector(thetaDeg)
	return dx*ux + dy*uy, -dx*uy + dy*ux
}

// ProjectFromTo 是 Project 的便捷封装：直接给源与受体坐标。
func ProjectFromTo(srcX, srcY, recX, recY, thetaDeg float64) (x, y float64) {
	return Project(recX-srcX, recY-srcY, thetaDeg)
}

// ValidAngle 报告风向角是否为有限数值（角度本身任意实数均可，会归一化）。
func ValidAngle(thetaDeg float64) error {
	if math.IsNaN(thetaDeg) || math.IsInf(thetaDeg, 0) {
		return fmt.Errorf("风向角 wind_angle_deg=%g 必须为有限数值", thetaDeg)
	}
	return nil
}
