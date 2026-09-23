// Package multisource 在单源能力之上叠一层：多点源稳态合成。
//
// 分层（各自独立文件，不堆进既有处理流程）：
//
//	source.go     单源对单受体的贡献：源级校验、坐标投影、逐源有效源高、调烟羽公式
//	aggregate.go  多源贡献的逐项累加与占比拆解（纯函数）
//	service.go    作业编排：作业级校验、逐受体汇总、落库与回查
//
// 复用约定：浓度公式（plume）、扩散参数（dispersion）、热抬升（rise）
// 的数学一字不改，原样调用；坐标投影统一走 projection 包的单一风向角约定。
package multisource

import (
	"fmt"
	"math"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/plume"
	"gaussian-plume/internal/projection"
	"gaussian-plume/internal/rise"
)

// 源贡献状态取值。
const (
	StatusOK      = "ok"      // 正常算出贡献
	StatusZero    = "zero"    // 受体不在该源下风半平面，贡献为零
	StatusInvalid = "invalid" // 源输入非法，按零贡献处理
)

// validateSource 校验单个源的排放条件；返回空串表示合法。
// 只拦源级字段（源强、物理源高、坐标、抬升参数），作业级字段在 service 层拦。
func validateSource(src model.PointSource) string {
	bad := func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
	switch {
	case bad(src.X) || bad(src.Y):
		return "源水平坐标必须为有限数值"
	case bad(src.Q):
		return "源强必须为有限数值"
	case src.Q < 0:
		return fmt.Sprintf("源强 Q=%g 不能为负", src.Q)
	case bad(src.PhysicalH):
		return "物理源高必须为有限数值"
	case src.PhysicalH < 0:
		return fmt.Sprintf("物理源高 physical_h=%g 不能为负", src.PhysicalH)
	}
	if r := src.BuoyancyRise; r != nil {
		switch {
		case bad(r.Ds) || r.Ds <= 0:
			return fmt.Sprintf("烟囱内径 ds=%g 必须为正", r.Ds)
		case bad(r.Vs) || r.Vs < 0:
			return fmt.Sprintf("出口速度 vs=%g 不能为负", r.Vs)
		case bad(r.Ts) || r.Ts <= 0:
			return fmt.Sprintf("烟气温度 ts=%g 必须为正(K)", r.Ts)
		case bad(r.Ta) || r.Ta <= 0:
			return fmt.Sprintf("环境温度 ta=%g 必须为正(K)", r.Ta)
		case r.Ts <= r.Ta:
			return fmt.Sprintf("热抬升需要 ts(%g)>ta(%g)", r.Ts, r.Ta)
		case bad(r.AmbientDTDz) || r.AmbientDTDz < 0:
			return "环境温度递减率不能为负或非有限值"
		}
	}
	return ""
}

// resolveEffectiveH 解析单个源对某个下风距离的有效源高：
// 未挂抬升时 H=hs；挂抬升时 H=hs+ΔH，ΔH 按该源到该受体的下风距离 x'
// 走 1/3 律截断（与单源接口按受体下风距离截断的语义一致）。
func resolveEffectiveH(src model.PointSource, u float64, stability model.Stability, downwindX float64) (h, dh float64, err error) {
	if src.BuoyancyRise == nil {
		return src.PhysicalH, 0, nil
	}
	in := *src.BuoyancyRise
	in.Stability = stability
	if in.U == 0 {
		in.U = u
	}
	in.X = downwindX
	h, dh, _, _, err = rise.EffectiveHeight(src.PhysicalH, in)
	return h, dh, err
}

// SourceContribution 计算单个源对单个受体点的贡献。
//
// 流程：源级校验 → 投影（projection 包，唯一风向角约定）→ 逐源有效源高 →
// 调 plume.Calculate 原公式。x'≤0（受体不在该源下风半平面）时按稳态解在
// 上风/侧风半平面的自然延拓取零贡献，不另写"丢弃该源"的分支。
//
// 返回的 contribution 已填好除 Share 外的全部字段；Share 由聚合层统一拆解。
func SourceContribution(src model.PointSource, srcIndex int, rec model.ReceptorInput, windAngleDeg, u float64, stability model.Stability) model.SourceContribution {
	out := model.SourceContribution{
		SourceIndex: srcIndex,
		SourceName:  src.Name,
		Status:      StatusOK,
	}

	if msg := validateSource(src); msg != "" {
		out.Status, out.Reason = StatusInvalid, msg
		return out
	}

	x, y := projection.ProjectFromTo(src.X, src.Y, rec.X, rec.Y, windAngleDeg)
	out.DownwindX, out.CrosswindY = x, y

	if !(x > 0) {
		// 正侧风（x'=0）或上风（x'<0）：稳态烟羽解在该半平面自然为零。
		out.Status = StatusZero
		out.Reason = "受体不在该源下风半平面（x'≤0），贡献为零"
		out.EffectiveH = src.PhysicalH
		return out
	}

	h, dh, err := resolveEffectiveH(src, u, stability, x)
	if err != nil {
		out.Status, out.Reason = StatusInvalid, "抬升计算失败: "+err.Error()
		return out
	}
	out.EffectiveH, out.DeltaH = h, dh

	res, err := plume.Calculate(model.PlumeParams{
		Q:          src.Q,
		EffectiveH: h,
		U:          u,
		Stability:  stability,
		X:          x,
		Y:          y,
		Z:          0, // 地面受体
	})
	if err != nil {
		out.Status, out.Reason = StatusInvalid, "浓度计算失败: "+err.Error()
		return out
	}
	out.C = res.C
	return out
}
