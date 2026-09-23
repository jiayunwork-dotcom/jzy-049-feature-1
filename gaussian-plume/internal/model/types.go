// Package model 定义稳态高斯烟羽扩散服务用到的核心领域类型与单位约定。
//
// 单位约定（全服务统一）：
//   - 源强 Q：kg/s
//   - 长度（源高、距离网格、σy/σz）：m
//   - 风速 u：m/s
//   - 温差 ΔT、环境温度 Ta：K
//   - 输出地面浓度 C：kg/m^3（1 kg/m^3 = 1e6 mg/m^3 = 1e9 µg/m^3）
package model

import "strings"

// Stability 是 Pasquill–Gifford 大气稳定度类别，取值 A–F：
// A=极不稳定，B=不稳定，C=弱不稳定，D=中性，E=弱稳定，F=稳定。
type Stability string

const (
	StabilityA Stability = "A"
	StabilityB Stability = "B"
	StabilityC Stability = "C"
	StabilityD Stability = "D"
	StabilityE Stability = "E"
	StabilityF Stability = "F"
)

// Valid 报告稳定度类别是否落在 A–F。
func (s Stability) Valid() bool {
	switch s {
	case StabilityA, StabilityB, StabilityC, StabilityD, StabilityE, StabilityF:
		return true
	}
	return false
}

// Normalize 把小写稳定度类别归一化为大写；非法值原样返回交由校验处理。
func (s Stability) Normalize() Stability {
	return Stability(strings.ToUpper(strings.TrimSpace(string(s))))
}

// PlumeParams 是一次高斯烟羽计算所需的全部物理输入。
type PlumeParams struct {
	Q          float64   `json:"q"`           // 源强 kg/s（持续点源，稳态）
	EffectiveH float64   `json:"effective_h"` // 有效源高 H = 物理源高 hs + 抬升 ΔH，m
	U          float64   `json:"u"`           // 烟羽高度处平均风速 m/s（约定 σy/σz 不显式依赖 u）
	Stability  Stability `json:"stability"`   // Pasquill–Gifford 稳定度类别 A–F
	X          float64   `json:"x"`           // 下风向距离 m
	Y          float64   `json:"y"`           // 横风向偏移 m（地面扫描轴线点取 0）
	Z          float64   `json:"z"`           // 受体高度 m（地面受体取 0）
}

// PlumeResult 是单个受体点的计算结果。
type PlumeResult struct {
	C          float64 `json:"c"`           // 地面（受体）浓度 kg/m^3
	SigmaY     float64 `json:"sigma_y"`     // 横向扩散参数 m
	SigmaZ     float64 `json:"sigma_z"`     // 垂向扩散参数 m
	EffectiveH float64 `json:"effective_h"` // 本次实际使用的有效源高 m
}

// RiseInput 是 Briggs 热（浮力）抬升所需的输入。
type RiseInput struct {
	Vs          float64   `json:"vs"`                     // 烟气出口速度 m/s
	Ds          float64   `json:"ds"`                     // 烟囱出口内径 m
	Ts          float64   `json:"ts"`                     // 烟气温度 K
	Ta          float64   `json:"ta"`                     // 环境温度 K
	U           float64   `json:"u,omitempty"`            // 烟囱出口处风速 m/s，0 时取作业风速
	Stability   Stability `json:"stability,omitempty"`    // 稳定度类别（由作业级填入）
	X           float64   `json:"x,omitempty"`            // 下风向距离 m；<=0 取最终抬升
	AmbientDTDz float64   `json:"ambient_dtdz,omitempty"` // 环境温度垂直递减率 -dTa/dz，K/m，仅稳定时需要
}

// ScanPointInput 是一条下风向扫描网格中单个受体点的输入。
type ScanPointInput struct {
	X float64 `json:"x"` // 下风向距离 m（必须 >0）
	Y float64 `json:"y"` // 横风向偏移 m，缺省 0
	Z float64 `json:"z"` // 受体高度 m，缺省 0
}

// ScanPointResult 是单个网格点的结果；非法点用 Error 说明，其余字段照算。
type ScanPointResult struct {
	Index  int      `json:"index"`
	X      float64  `json:"x"`
	Y      float64  `json:"y"`
	Z      float64  `json:"z"`
	C      *float64 `json:"c,omitempty"`
	SigmaY *float64 `json:"sigma_y,omitempty"`
	SigmaZ *float64 `json:"sigma_z,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// ScanRequest 是一条完整下风向扫描作业的输入与作业级排放条件。
type ScanRequest struct {
	Q            float64          `json:"q"`                       // 源强 kg/s
	PhysicalH    float64          `json:"physical_h"`              // 物理烟囱高度 hs m
	U            float64          `json:"u"`                       // 风速 m/s
	Stability    Stability        `json:"stability"`               // 稳定度类别 A–F
	Points       []ScanPointInput `json:"points"`                  // 下风向距离网格
	BuoyancyRise *RiseInput       `json:"buoyancy_rise,omitempty"` // 可选：叠 Briggs 热抬升
	// EffectiveH 可由调用方直接给定；与抬升互斥（校验阶段判定）。
	EffectiveH float64 `json:"effective_h,omitempty"`
	UseGivenH  bool    `json:"use_given_h,omitempty"`
}

// ScanResult 是一次扫描的整体结果。
type ScanResult struct {
	Status     string            `json:"status"` // "ok" 全部点成功；"partial" 存在非法点
	EffectiveH float64           `json:"effective_h"`
	DeltaH     float64           `json:"delta_h,omitempty"`
	Points     []ScanPointResult `json:"points"`
}

// JobRecord 是落库、可回查的扫描作业。
type JobRecord struct {
	ID        string      `json:"id"`
	Request   ScanRequest `json:"request"`
	Result    ScanResult  `json:"result"`
	CreatedAt string      `json:"created_at"`
}
