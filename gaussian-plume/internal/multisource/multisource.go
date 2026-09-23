// Package multisource 是长在现有单点能力之上的「多点源叠加」一层：
//
//	projection（坐标投影，新骨架）
//	  → 对每个源逐个解析有效源高（rise，原封不动）
//	  → plume.Calculate（高斯烟羽公式/扩散参数，原封不动）
//	  → 在同一受体点上把各源贡献相加（本包）
//	  → 拆出每个源的贡献与占比（本包）
//
// 数学公式、扩散参数、热抬升一个字不改：每个源在投影出自己的
// (downwind x, crosswind y) 后，直接套用现有单点公式求该源单独贡献。
// 线性叠加原理（稳态高斯解对源强线性）保证：各源单独算得的贡献之和，
// 恰好等于整批一起提交时的合成浓度。
//
// 逐源容错：批量中某个源的排放条件非法（源强为负、物理源高为负、抬升参数
// 非法等），只把该源在本次作业里按【零贡献】处理并写明原因，其余源照常
// 参与叠加，受体合成结果与作业落库都不因一个坏源整体失败。
package multisource

import (
	"math"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/plume"
	"gaussian-plume/internal/projection"
	"gaussian-plume/internal/rise"
)

// ValidateSource 校验单个点源；返回问题说明（空串表示合法）。
// 仅覆盖该源自身的排放条件；风速/稳定度/风向是作业级公共条件，在服务层校验。
func ValidateSource(src model.PointSourceInput) string {
	if math.IsNaN(src.X) || math.IsInf(src.X, 0) || math.IsNaN(src.Y) || math.IsInf(src.Y, 0) {
		return "源水平坐标必须为有限数值"
	}
	if math.IsNaN(src.Q) || math.IsInf(src.Q, 0) {
		return "源强必须为有限数值"
	}
	if src.Q < 0 {
		return "源强 Q 不能为负"
	}
	if math.IsNaN(src.PhysicalH) || math.IsInf(src.PhysicalH, 0) {
		return "物理源高必须为有限数值"
	}
	if src.PhysicalH < 0 {
		return "物理源高 physical_h 不能为负"
	}
	if src.UseGivenH && src.BuoyancyRise != nil {
		return "effective_h 与 buoyancy_rise 互斥，不能同时给定"
	}
	if src.UseGivenH && (src.EffectiveH < 0 || math.IsNaN(src.EffectiveH) || math.IsInf(src.EffectiveH, 0)) {
		return "给定有效源高 effective_h 不能为负或非有限值"
	}
	if r := src.BuoyancyRise; r != nil {
		if math.IsNaN(r.Vs) || math.IsInf(r.Vs, 0) || r.Vs < 0 {
			return "热抬升出口速度 vs 不能为负或非有限值"
		}
		if math.IsNaN(r.Ds) || math.IsInf(r.Ds, 0) || r.Ds <= 0 {
			return "热抬升烟囱内径 ds 必须为正"
		}
		if math.IsNaN(r.Ts) || math.IsInf(r.Ts, 0) || r.Ts <= 0 {
			return "热抬升烟气温度 ts 必须为正(K)"
		}
		if math.IsNaN(r.Ta) || math.IsInf(r.Ta, 0) || r.Ta <= 0 {
			return "热抬升环境温度 ta 必须为正(K)"
		}
		if r.Ts > 0 && r.Ta > 0 && r.Ts <= r.Ta {
			return "热抬升需要烟气温度 ts>环境温度 ta"
		}
		if math.IsNaN(r.AmbientDTDz) || math.IsInf(r.AmbientDTDz, 0) || r.AmbientDTDz < 0 {
			return "环境温度递减率不能为负或非有限值"
		}
	}
	return ""
}

// resolveHeight 解析单个源在给定下风距离处的有效源高，逻辑与现有单点/扫描
// 编排保持同一种选择：给定 effective_h 用之；给 buoyancy_rise 叠抬升；
// 否则物理源高即有效源高。返回 (H, ΔH, regime)。
func resolveHeight(src model.PointSourceInput, u float64, stab model.Stability, downwindX float64) (h, dh float64, regime string, err error) {
	if src.UseGivenH {
		return src.EffectiveH, 0, "", nil
	}
	if src.BuoyancyRise != nil {
		in := *src.BuoyancyRise
		if !(in.U > 0) {
			in.U = u // 抬升风速缺省取作业统一风速
		}
		in.Stability = stab
		in.X = downwindX // 有限距离截断：用投影后的沿风下风分量
		hh, d, _, rg, e := rise.EffectiveHeight(src.PhysicalH, in)
		if e != nil {
			return 0, 0, "", e
		}
		return hh, d, rg, nil
	}
	return src.PhysicalH, 0, "", nil
}

// contribution 计算单个源在单个受体点上的地面（z=0）贡献。
// 该函数只做投影 + 复用现有公式，不修改任何既有数学。
//
// 投影落在烟羽不覆盖的半平面（downwind<=0，受体在源上风/正侧风）时，
// 不做方位特判删除该源，而是按稳态解的自然极限返回 0 贡献（不带错误）。
func contribution(src model.PointSourceInput, rec model.ReceptorInput,
	windDir, u float64, stab model.Stability) model.SourceContribution {

	rel := projection.Project(
		projection.XY{X: src.X, Y: src.Y},
		projection.XY{X: rec.X, Y: rec.Y},
		windDir,
	)
	out := model.SourceContribution{
		SourceID:   src.ID,
		DownwindX:  rel.Downwind,
		CrosswindY: rel.Crosswind,
	}
	if rel.Downwind <= 0 {
		// 上风/正侧风：烟羽稳态解只定义在下风半平面，贡献自然为 0。
		return out
	}
	h, dh, regime, err := resolveHeight(src, u, stab, rel.Downwind)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	res, err := plume.Calculate(model.PlumeParams{
		Q:          src.Q,
		EffectiveH: h,
		U:          u,
		Stability:  stab,
		X:          rel.Downwind,
		Y:          rel.Crosswind,
		Z:          0, // 地面受体
	})
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sy, sz, hh := res.SigmaY, res.SigmaZ, res.EffectiveH
	out.C = res.C
	out.SigmaY, out.SigmaZ, out.EffectiveH = &sy, &sz, &hh
	if src.BuoyancyRise != nil {
		out.DeltaH = &dh
		out.RiseRegime = regime
	}
	return out
}

// Compute 对一个受体点逐项累加各源贡献并拆占比。返回受体点合成结果。
// anyBad 报告是否存在被按零贡献处理的问题源（由调用方汇总成作业状态）。
func Compute(req model.MultiSourceRequest, rec model.ReceptorInput, recIndex int) (rr model.ReceptorResult, anyBad bool) {
	rr = model.ReceptorResult{
		ReceptorID:    rec.ID,
		Index:         recIndex,
		X:             rec.X,
		Y:             rec.Y,
		Contributions: make([]model.SourceContribution, 0, len(req.Sources)),
	}
	total := 0.0
	for i, src := range req.Sources {
		var sc model.SourceContribution
		if msg := ValidateSource(src); msg != "" {
			// 源级排放条件非法：仍照常投影（保留几何位置可回查），但只按零
			// 贡献处理并说明原因，不进入烟羽公式，不牵连其余源。
			rel := projection.Project(
				projection.XY{X: src.X, Y: src.Y},
				projection.XY{X: rec.X, Y: rec.Y},
				req.WindDir,
			)
			sc = model.SourceContribution{
				SourceID:   src.ID,
				DownwindX:  rel.Downwind,
				CrosswindY: rel.Crosswind,
				Error:      msg,
			}
			anyBad = true
		} else {
			sc = contribution(src, rec, req.WindDir, req.U, req.Stability)
			if sc.Error != "" {
				// 校验形式通过但计算/抬升仍失败（数值异常等）：同样隔离该源。
				sc.C = 0
				sc.SigmaY, sc.SigmaZ, sc.EffectiveH, sc.DeltaH = nil, nil, nil, nil
				sc.RiseRegime = ""
				anyBad = true
			}
		}
		sc.Index = i
		rr.Contributions = append(rr.Contributions, sc)
		total += sc.C
	}
	rr.C = total
	// 占比拆解：合成浓度为 0（全在上风侧或全是坏源）时统一取 0，避免除零。
	for i := range rr.Contributions {
		if total > 0 {
			rr.Contributions[i].Share = rr.Contributions[i].C / total
		}
	}
	return rr, anyBad
}

// Run 执行一次多点源叠加计算（不落库）。假设作业级公共条件已由服务层
// 校验（风速为正、稳定度合法、风向角有限、源/受体列表非空、坐标有限）。
func Run(req model.MultiSourceRequest) model.MultiSourceResult {
	res := model.MultiSourceResult{
		WindDir:   req.WindDir,
		U:         req.U,
		Stability: req.Stability,
		Receptors: make([]model.ReceptorResult, 0, len(req.Receptors)),
	}
	allOK := true
	for i, rec := range req.Receptors {
		rr, bad := Compute(req, rec, i)
		if bad {
			allOK = false
		}
		res.Receptors = append(res.Receptors, rr)
	}
	if allOK {
		res.Status = "ok"
	} else {
		res.Status = "partial"
	}
	return res
}
