// Package projection 是多点源叠加层的坐标骨架：把共享平面坐标系里的
// 「源 → 受体」位移，按本次作业统一给定的风向角，投影成现有单点烟羽公式
// 所需的沿风下风分量 x 与垂直风向侧风分量 y。
//
// 钉死的风向角约定（全服务只此一种，所有源/受体统一套用，不得在别处
// 再按数学角或别的罗盘约定另算一遍）：
//
//	wind_dir：气象罗盘风向角，单位「度」，从正北起算、顺时针增加，
//	          定义为风的【来向】（与气象观测口径一致：0°=北风，
//	          90°=东风，180°=南风，270°=西风）。
//	          烟羽的输送（下风）方向取其反方向 θ = wind_dir + 180°。
//
// 平面坐标系：x 轴朝东、y 轴朝北（右手平面，单位 m）。
//
// 设下风输送方位角 θ（自正北顺时针），则单位矢量：
//
//	e_down = (sin θ, cos θ)                  沿下风（东, 北）
//	e_cross = (cos θ, −sin θ)                垂直下风，面向下风时右侧为正
//
// 对源 S 到受体 R 的位移 d = R − S：
//
//	x = d · e_down   （>0：受体在源下风向；≤0：受体不在烟羽覆盖的半平面）
//	y = d · e_cross  （横向偏移，浓度只依赖 y²，左右对称）
//
// 不做任何「按方位角剔除源」的特判：所有源统一走投影 + 现有烟羽公式这一条
// 路径。受体在源上风（x<0）时烟羽稳态解不覆盖该半平面，贡献按其极限取 0；
// 受体在正侧风（x=0）时同样由投影结果自然落到 0。源仍逐项出现在贡献清单里
// （贡献为 0 且不带错误），而不是被悄悄丢掉。
package projection

import (
	"fmt"
	"math"
)

// FullCircle 是角度归一化周期。
const FullCircle = 360.0

// NormalizeWindDir 把任意（可为负、可多圈）风向角归一化到 [0,360)。
// NaN/Inf 无几何意义，返回错误。
func NormalizeWindDir(deg float64) (float64, error) {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return 0, fmt.Errorf("风向角 %g 必须为有限数值（度）", deg)
	}
	d := math.Mod(deg, FullCircle)
	if d < 0 {
		d += FullCircle
	}
	return d, nil
}

// NormalizeBearing 把任意方位角（自正北顺时针，度）归一化到 [0,360)。
func NormalizeBearing(deg float64) float64 {
	d := math.Mod(deg, FullCircle)
	if d < 0 {
		d += FullCircle
	}
	return d
}

// XY 是共享平面坐标系中的一个水平位置（东向 x、北向 y，m）。
// 与 model.XY 字段对齐，本包不依赖 model 以保持骨架独立。
type XY struct {
	X float64 // 东向 m
	Y float64 // 北向 m
}

// Relative 是「源 → 受体」位移在风轴坐标系下的投影。
type Relative struct {
	Downwind  float64 // x：沿下风分量 m（受体在源上风为负）
	Crosswind float64 // y：垂直下风分量 m（面向下风右侧为正；浓度只依赖其平方）
}

// DownwindBearing 返回烟羽输送方向（下风方位角，自正北顺时针，度）：
// 风向来向 wind_dir 的反方向。
func DownwindBearing(windDirDeg float64) float64 {
	return NormalizeBearing(windDirDeg + FullCircle/2)
}

// Project 按给定气象罗盘风向角（来向，度，自正北顺时针）把源到受体的
// 水平位移投影成 (沿风下风 x, 侧风 y)。
func Project(source, receptor XY, windDirDeg float64) Relative {
	theta := DownwindBearing(windDirDeg) * math.Pi / 180
	sin, cos := math.Sin(theta), math.Cos(theta)
	dx := receptor.X - source.X
	dy := receptor.Y - source.Y
	return Relative{
		Downwind:  dx*sin + dy*cos,
		Crosswind: dx*cos - dy*sin,
	}
}
