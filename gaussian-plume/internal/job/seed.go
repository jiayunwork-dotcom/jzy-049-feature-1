// Package job 的 seed.go 负责预置地面源示范作业。
//
// 参数刻意挑成易手工核对：有效源高 H=0（地面源）、轴线(y=0)、地面(z=0)、
// D 类中性，解析极限 C = Q/(π·σy·σz·u)。
//
// 取 Q=0.1 kg/s，u=5 m/s，x=1000 m（D 类 P-G 锚定值 σy=70、σz=26）：
//
//	C = 0.1/(π·70·26·5) ≈ 3.498e-6 kg/m^3 ≈ 3498 µg/m^3
//
// 该值可与通用式在 H=0 极限下逐项对上（镜像项+直接项合并为 2）。
package job

import (
	"context"

	"gaussian-plume/internal/model"
)

// DemoJobID 是预置示范作业的固定标识（幂等：重启不重复插入）。
const DemoJobID = "00000000-0000-0000-0000-000000000001"

// demoDistances 是示范作业的下风向距离网格（m）。
var demoDistances = []float64{100, 200, 500, 1000, 2000, 5000}

// DemoRequest 返回地面源示范作业的输入。
func DemoRequest() model.ScanRequest {
	pts := make([]model.ScanPointInput, 0, len(demoDistances))
	for _, x := range demoDistances {
		pts = append(pts, model.ScanPointInput{X: x})
	}
	return model.ScanRequest{
		Q:          0.1,
		PhysicalH:  0,
		U:          5,
		Stability:  model.StabilityD,
		Points:     pts,
		UseGivenH:  true,
		EffectiveH: 0, // 地面源
	}
}

// SeedDemo 幂等预置地面源示范作业；已存在则不覆盖。
func (s *Service) SeedDemo(ctx context.Context) error {
	if _, err := s.Get(ctx, DemoJobID); err == nil {
		return nil // 已预置
	} else {
		if _, ok := err.(ErrNotFound); !ok {
			return err
		}
	}
	_, err := s.Submit(ctx, DemoRequest(), DemoJobID)
	return err
}
