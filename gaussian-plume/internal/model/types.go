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

// ---- 多点源叠加（稳态合成）----

// PointSource 是一根排放烟囱：水平位置 + 排放条件 + 物理源高，热抬升参数可选挂。
// 水平坐标 (X,Y) 与受体点共用同一个平面坐标系（单位 m，原点任意但全作业一致）。
type PointSource struct {
	Name         string     `json:"name,omitempty"`          // 可选源标签，回查/定位用
	X            float64    `json:"x"`                       // 源水平坐标 x（m）
	Y            float64    `json:"y"`                       // 源水平坐标 y（m）
	Q            float64    `json:"q"`                       // 源强 kg/s
	PhysicalH    float64    `json:"physical_h"`              // 物理烟囱高度 hs m
	BuoyancyRise *RiseInput `json:"buoyancy_rise,omitempty"` // 可选：该源叠 Briggs 热抬升
}

// ReceptorInput 是一个受体点（厂区边界、最近居民点等）的水平坐标。
type ReceptorInput struct {
	Name string  `json:"name,omitempty"` // 可选受体标签
	X    float64 `json:"x"`              // 受体水平坐标 x（m）
	Y    float64 `json:"y"`              // 受体水平坐标 y（m）
}

// MultiSourceRequest 是一次多源稳态合成作业的完整输入。
// 风向角约定（全服务唯一，见 projection 包文档）：wind_angle_deg 为风**吹向**
// 的方位角，自共享坐标系 +x 轴起算、逆时针为正、单位度；所有源与受体统一套用。
type MultiSourceRequest struct {
	WindAngleDeg float64         `json:"wind_angle_deg"` // 风吹向方位角（度，数学约定）
	U            float64         `json:"u"`              // 风速 m/s（全部源共用）
	Stability    Stability       `json:"stability"`      // 稳定度类别 A–F（全部源共用）
	Sources      []PointSource   `json:"sources"`        // 点源批（≥1）
	Receptors    []ReceptorInput `json:"receptors"`      // 受体点批（≥1）
}

// SourceContribution 是单个源对单个受体点的贡献拆解。
type SourceContribution struct {
	SourceIndex int     `json:"source_index"` // 源在请求批中的下标
	SourceName  string  `json:"source_name,omitempty"`
	DownwindX   float64 `json:"downwind_x"`        // 投影后的下风分量 x'（m）
	CrosswindY  float64 `json:"crosswind_y"`       // 投影后的侧风分量 y'（m）
	EffectiveH  float64 `json:"effective_h"`       // 该源本次实际使用的有效源高 m
	DeltaH      float64 `json:"delta_h,omitempty"` // 热抬升量（未挂抬升为 0）
	C           float64 `json:"c"`                 // 该源在该受体的地面浓度 kg/m^3
	Share       float64 `json:"share"`             // 占该受体合成浓度的份额 [0,1]
	Status      string  `json:"status"`            // "ok" | "zero"（受体不在该源下风半平面）| "invalid"
	Reason      string  `json:"reason,omitempty"`  // zero/invalid 时的说明
}

// ReceptorResult 是单个受体点的合成结果：总浓度 + 逐源拆解。
type ReceptorResult struct {
	Index         int                  `json:"index"`
	ReceptorName  string               `json:"receptor_name,omitempty"`
	X             float64              `json:"x"`
	Y             float64              `json:"y"`
	TotalC        float64              `json:"total_c"` // 全部源合成地面浓度 kg/m^3
	Contributions []SourceContribution `json:"contributions,omitempty"`
	Status        string               `json:"status"` // "ok" | "partial"（含非法源）| "invalid"（受体坐标非法）
	Error         string               `json:"error,omitempty"`
}

// MultiSourceResult 是一次多源合成作业的整体结果。
type MultiSourceResult struct {
	Status       string           `json:"status"` // "ok" | "partial"
	WindAngleDeg float64          `json:"wind_angle_deg"`
	U            float64          `json:"u"`
	Stability    Stability        `json:"stability"`
	Receptors    []ReceptorResult `json:"receptors"`
}

// MultiSourceJobRecord 是落库、可回查的多源合成作业。
type MultiSourceJobRecord struct {
	ID        string             `json:"id"`
	Request   MultiSourceRequest `json:"request"`
	Result    MultiSourceResult  `json:"result"`
	CreatedAt string             `json:"created_at"`
}
