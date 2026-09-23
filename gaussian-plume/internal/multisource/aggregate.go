package multisource

import "gaussian-plume/internal/model"

// Aggregate 把一个受体点上全部源的贡献逐项累加，并拆解每个源的占比。
//
// 叠加原理：稳态高斯烟羽对源强是线性的，多源共存时同一受体点的地面浓度
// 等于各源单独贡献之和（线性叠加，本服务钉死的合成规则）。
//
// 占比拆解：share_i = c_i / Σc；Σc=0（全部源贡献均为零）时所有占比取 0，
// 避免除零。返回 (合成浓度, 是否存在非法源)。
func Aggregate(contributions []model.SourceContribution) (total float64, hasInvalid bool) {
	for i := range contributions {
		if contributions[i].Status == StatusInvalid {
			hasInvalid = true
			continue // 非法源按零贡献处理，不参与累加
		}
		total += contributions[i].C
	}
	if total > 0 {
		for i := range contributions {
			if contributions[i].Status == StatusInvalid {
				contributions[i].Share = 0
				continue
			}
			contributions[i].Share = contributions[i].C / total
		}
	}
	return total, hasInvalid
}
