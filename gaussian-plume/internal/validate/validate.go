// Package validate 集中处理进计算前的输入校验，输出结构化错误说明。
//
// 作业级（整单非法 -> 400，不产生任何网格结果）：
//   - 风速非正；源强为负；稳定度不在 A–F；物理源高为负；距离网格为空；
//   - 抬升参数非法；同时给出 effective_h 又给 buoyancy_rise 的冲突。
//
// 点位级（个别网格点非法 -> 该点给错误说明，其余照常算完）：
//   - 下风向距离非正；y/z 为 NaN。
package validate

import (
	"fmt"
	"math"
	"strings"

	"gaussian-plume/internal/model"
)

// FieldError 是结构化字段错误。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e FieldError) Error() string { return e.Field + ": " + e.Reason }

// Errors 聚合多个字段错误，实现 error。
type Errors []FieldError

func (es Errors) Error() string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

func nonFinite(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }

// validateCommon 校验源强/风速/稳定度/物理源高这些作业级公共字段。
func validateCommon(q, u, physicalH float64, stability model.Stability, errs *Errors) {
	if nonFinite(q) {
		*errs = append(*errs, FieldError{"q", "源强必须为有限数值"})
	} else if q < 0 {
		*errs = append(*errs, FieldError{"q", fmt.Sprintf("源强 Q=%g 不能为负", q)})
	}
	if nonFinite(u) {
		*errs = append(*errs, FieldError{"u", "风速必须为有限数值"})
	} else if u <= 0 {
		*errs = append(*errs, FieldError{"u", fmt.Sprintf("风速 u=%g 必须为正", u)})
	}
	st := stability.Normalize()
	if !st.Valid() {
		*errs = append(*errs, FieldError{"stability", fmt.Sprintf("稳定度类别 %q 不在 A–F 之间", stability)})
	}
	if nonFinite(physicalH) {
		*errs = append(*errs, FieldError{"physical_h", "物理源高必须为有限数值"})
	} else if physicalH < 0 {
		*errs = append(*errs, FieldError{"physical_h", fmt.Sprintf("物理源高 physical_h=%g 不能为负", physicalH)})
	}
}

func validateRise(r model.RiseInput, errs *Errors) {
	if r.Ds <= 0 || nonFinite(r.Ds) {
		*errs = append(*errs, FieldError{"buoyancy_rise.ds", fmt.Sprintf("烟囱内径 ds=%g 必须为正", r.Ds)})
	}
	if r.Vs < 0 || nonFinite(r.Vs) {
		*errs = append(*errs, FieldError{"buoyancy_rise.vs", fmt.Sprintf("出口速度 vs=%g 不能为负", r.Vs)})
	}
	if r.Ts <= 0 || nonFinite(r.Ts) {
		*errs = append(*errs, FieldError{"buoyancy_rise.ts", fmt.Sprintf("烟气温度 ts=%g 必须为正(K)", r.Ts)})
	}
	if r.Ta <= 0 || nonFinite(r.Ta) {
		*errs = append(*errs, FieldError{"buoyancy_rise.ta", fmt.Sprintf("环境温度 ta=%g 必须为正(K)", r.Ta)})
	}
	if r.Ts > 0 && r.Ta > 0 && r.Ts <= r.Ta {
		*errs = append(*errs, FieldError{"buoyancy_rise.ts", fmt.Sprintf("热抬升需要 ts(%g)>ta(%g)", r.Ts, r.Ta)})
	}
	if r.AmbientDTDz < 0 || nonFinite(r.AmbientDTDz) {
		*errs = append(*errs, FieldError{"buoyancy_rise.ambient_dtdz", "环境温度递减率不能为负或非有限值"})
	}
}

// Point 校验单个受体点；返回该点的错误说明（空串表示合法）。
func Point(pt model.ScanPointInput) string {
	if nonFinite(pt.X) {
		return "下风向距离必须为有限数值"
	}
	if pt.X <= 0 {
		return fmt.Sprintf("下风向距离 x=%g 必须为正", pt.X)
	}
	if nonFinite(pt.Y) {
		return "横风向偏移 y 必须为有限数值"
	}
	if nonFinite(pt.Z) {
		return "受体高度 z 必须为有限数值"
	}
	if pt.Z < 0 {
		return fmt.Sprintf("受体高度 z=%g 不能为负", pt.Z)
	}
	return ""
}

// SingleRequest 是单点计算接口的入参（JSON 见 api 层）。
type SingleRequest struct {
	Q            float64          `json:"q"`
	PhysicalH    float64          `json:"physical_h"`
	U            float64          `json:"u"`
	Stability    model.Stability  `json:"stability"`
	X            float64          `json:"x"`
	Y            float64          `json:"y"`
	Z            float64          `json:"z"`
	EffectiveH   float64          `json:"effective_h"`
	UseGivenH    bool             `json:"use_given_h"`
	BuoyancyRise *model.RiseInput `json:"buoyancy_rise,omitempty"`
}

// Single 校验单点请求。
func Single(req SingleRequest) error {
	var errs Errors
	validateCommon(req.Q, req.U, req.PhysicalH, req.Stability, &errs)
	if nonFinite(req.X) {
		errs = append(errs, FieldError{"x", "下风向距离必须为有限数值"})
	} else if req.X <= 0 {
		errs = append(errs, FieldError{"x", fmt.Sprintf("下风向距离 x=%g 必须为正", req.X)})
	}
	if nonFinite(req.Y) {
		errs = append(errs, FieldError{"y", "横风向偏移 y 必须为有限数值"})
	}
	if nonFinite(req.Z) {
		errs = append(errs, FieldError{"z", "受体高度 z 必须为有限数值"})
	} else if req.Z < 0 {
		errs = append(errs, FieldError{"z", fmt.Sprintf("受体高度 z=%g 不能为负", req.Z)})
	}
	if req.UseGivenH && req.BuoyancyRise != nil {
		errs = append(errs, FieldError{"effective_h", "effective_h 与 buoyancy_rise 互斥，不能同时给定"})
	}
	if req.UseGivenH && (req.EffectiveH < 0 || nonFinite(req.EffectiveH)) {
		errs = append(errs, FieldError{"effective_h", fmt.Sprintf("有效源高 effective_h=%g 不能为负", req.EffectiveH)})
	}
	if req.BuoyancyRise != nil {
		if req.BuoyancyRise.U == 0 {
			req.BuoyancyRise.U = req.U
		}
		req.BuoyancyRise.Stability = req.Stability.Normalize()
		validateRise(*req.BuoyancyRise, &errs)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Scan 校验扫描作业级字段。点位级非法不在此拦截（编排层逐点给说明）。
// 会把稳定性归一化后回写。
func Scan(req *model.ScanRequest) error {
	var errs Errors
	validateCommon(req.Q, req.U, req.PhysicalH, req.Stability, &errs)
	req.Stability = req.Stability.Normalize()
	if len(req.Points) == 0 {
		errs = append(errs, FieldError{"points", "下风向距离网格不能为空"})
	}
	if req.UseGivenH && req.BuoyancyRise != nil {
		errs = append(errs, FieldError{"effective_h", "effective_h 与 buoyancy_rise 互斥，不能同时给定"})
	}
	if req.UseGivenH && (req.EffectiveH < 0 || nonFinite(req.EffectiveH)) {
		errs = append(errs, FieldError{"effective_h", fmt.Sprintf("有效源高 effective_h=%g 不能为负", req.EffectiveH)})
	}
	if req.BuoyancyRise != nil {
		if req.BuoyancyRise.U == 0 {
			req.BuoyancyRise.U = req.U
		}
		req.BuoyancyRise.Stability = req.Stability
		validateRise(*req.BuoyancyRise, &errs)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}
