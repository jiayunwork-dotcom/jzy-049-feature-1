package projection

import (
	"math"
	"testing"
)

const eps = 1e-12

// 轴对齐：风朝 +x（θ=0°），受体在源正东 1000m —— 纯下风 1000m、侧风 0。
func TestProjectAxisAligned(t *testing.T) {
	x, y := ProjectFromTo(0, 0, 1000, 0, 0)
	if x != 1000 || y != 0 {
		t.Errorf("θ=0 正东受体: got (%v,%v), want (1000,0)", x, y)
	}
	// 源不在原点：平移不变性，只看相对位移。
	x, y = ProjectFromTo(500, 200, 1500, 200, 0)
	if x != 1000 || y != 0 {
		t.Errorf("平移后: got (%v,%v), want (1000,0)", x, y)
	}
}

// 风朝 +y（θ=90°）：受体在源正北为纯下风；在源正东为纯侧风。
func TestProjectRotated90(t *testing.T) {
	x, y := ProjectFromTo(0, 0, 0, 500, 90)
	if math.Abs(x-500) > eps || math.Abs(y) > eps {
		t.Errorf("θ=90 正北受体: got (%v,%v), want (500,0)", x, y)
	}
	// +y' 在风向左侧；θ=90° 时风向 +y（北），左侧为 -x（西），故正东受体 y'<0。
	x, y = ProjectFromTo(0, 0, 300, 0, 90)
	if math.Abs(x) > eps || math.Abs(y+300) > eps {
		t.Errorf("θ=90 正东受体应为纯侧风: got (%v,%v), want (0,-300)", x, y)
	}
}

// 上风位置投影出负的下风分量；正侧风投影出零下风分量。
func TestProjectUpwindAndCrosswind(t *testing.T) {
	x, _ := ProjectFromTo(0, 0, -100, 0, 0)
	if x != -100 {
		t.Errorf("受体在源正西（上风）: x'=%v, want -100", x)
	}
	x, y := ProjectFromTo(0, 0, 0, 100, 0)
	if x != 0 || y != 100 {
		t.Errorf("受体在源正北（θ=0 侧风）: got (%v,%v), want (0,100)", x, y)
	}
	// 侧风符号约定：+y' 在风向左侧。θ=0 时风向 +x，左侧为 +y（北）。
	_, y = ProjectFromTo(0, 0, 0, -100, 0)
	if y != -100 {
		t.Errorf("受体在源正南: y'=%v, want -100", y)
	}
}

// 任意角：位移恰好沿风向时长为下风分量，侧风分量为零；逆转 90° 后纯侧风。
func TestProjectArbitraryAngle(t *testing.T) {
	theta := 30.0
	ux, uy := WindVector(theta)
	if math.Abs(ux-math.Cos(math.Pi/6)) > eps || math.Abs(uy-0.5) > eps {
		t.Fatalf("WindVector(30)=(%v,%v)", ux, uy)
	}
	// 沿风向 800m 处的受体。
	x, y := ProjectFromTo(100, 50, 100+800*ux, 50+800*uy, theta)
	if math.Abs(x-800) > 1e-9 || math.Abs(y) > 1e-9 {
		t.Errorf("沿风向受体: got (%v,%v), want (800,0)", x, y)
	}
	// 风向左侧 200m 的受体（位移 = 左法向 ×200）。
	x, y = ProjectFromTo(0, 0, -200*uy, 200*ux, theta)
	if math.Abs(x) > 1e-9 || math.Abs(y-200) > 1e-9 {
		t.Errorf("风向左侧受体: got (%v,%v), want (0,200)", x, y)
	}
}

// 角度归一化：负角与超 360° 角都折回 [0,360)，且投影结果一致。
func TestNormalizeDegAndEquivalence(t *testing.T) {
	if got := NormalizeDeg(-90); got != 270 {
		t.Errorf("NormalizeDeg(-90)=%v, want 270", got)
	}
	if got := NormalizeDeg(450); got != 90 {
		t.Errorf("NormalizeDeg(450)=%v, want 90", got)
	}
	x1, y1 := ProjectFromTo(0, 0, 300, 400, -90)
	x2, y2 := ProjectFromTo(0, 0, 300, 400, 270)
	// cos/sin 在 -90° 与 270° 的末位舍入不同，容差内一致即可。
	if math.Abs(x1-x2) > 1e-9 || math.Abs(y1-y2) > 1e-9 {
		t.Errorf("-90° 与 270° 投影应一致: (%v,%v) vs (%v,%v)", x1, y1, x2, y2)
	}
}

// 非法风向角（NaN/Inf）必须被拦下。
func TestValidAngle(t *testing.T) {
	if err := ValidAngle(123.4); err != nil {
		t.Errorf("有限角应合法: %v", err)
	}
	if err := ValidAngle(math.NaN()); err == nil {
		t.Error("NaN 风向角应报错")
	}
	if err := ValidAngle(math.Inf(1)); err == nil {
		t.Error("Inf 风向角应报错")
	}
}
