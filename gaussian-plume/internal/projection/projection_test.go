package projection

import (
	"math"
	"testing"
)

const tol = 1e-12

// 钉死的罗盘约定：wind_dir=0 表示北风（风自北向南吹），下风方向为正南。
// 源在 (0,0)、受体在 (0,-100)（正南方）→ downwind=100, crosswind=0。
func TestProjectNorthWindDueSouthDownwind(t *testing.T) {
	rel := Project(XY{0, 0}, XY{0, -100}, 0)
	if math.Abs(rel.Downwind-100) > tol || math.Abs(rel.Crosswind) > tol {
		t.Fatalf("北风、受体在正南:  got (x=%v, y=%v), want (100, 0)", rel.Downwind, rel.Crosswind)
	}
	// 同一根烟羽轴线正北侧的受体：上风半平面，downwind 必须为负。
	up := Project(XY{0, 0}, XY{0, 100}, 0)
	if math.Abs(up.Downwind+100) > tol || math.Abs(up.Crosswind) > tol {
		t.Fatalf("北风、受体在正北（上风）: got (%v,%v), want (-100,0)", up.Downwind, up.Crosswind)
	}
	// 正东侧受体：北风时在烟羽正侧风方向，downwind=0，crosswind=-100。
	side := Project(XY{0, 0}, XY{100, 0}, 0)
	if math.Abs(side.Downwind) > tol || math.Abs(side.Crosswind+100) > tol {
		t.Fatalf("北风、受体在正东（正侧风）: got (%v,%v), want (0,-100)", side.Downwind, side.Crosswind)
	}
}

// wind_dir=90 表示东风（风自东向西吹）：正西受体落在下风轴线上。
func TestProjectEastWindDueWestDownwind(t *testing.T) {
	rel := Project(XY{0, 0}, XY{-250, 0}, 90)
	if math.Abs(rel.Downwind-250) > tol || math.Abs(rel.Crosswind) > tol {
		t.Fatalf("东风、受体在正西: got (%v,%v), want (250,0)", rel.Downwind, rel.Crosswind)
	}
}

// 斜风（北风 45°，即西北风）：下风朝东南，位移与下风轴重合时
// downwind=位移长度、crosswind=0。
func TestProjectDiagonalWind(t *testing.T) {
	// 西北风（来向 315°）→ 下风朝东南 (sin135,cos135)=(√2/2,-√2/2)。
	rel := Project(XY{0, 0}, XY{100, -100}, 315)
	want := 100 * math.Sqrt2
	if math.Abs(rel.Downwind-want) > 1e-9 || math.Abs(rel.Crosswind) > 1e-9 {
		t.Fatalf("西北风、受体东南: got (%v,%v), want (%v,0)", rel.Downwind, rel.Crosswind, want)
	}
}

// 左右对称：侧风分量取正负号不影响 |y|，且下风分量与源位置无关性正确。
func TestProjectCrosswindSignSymmetry(t *testing.T) {
	east := Project(XY{0, 0}, XY{100, -100}, 0)
	west := Project(XY{0, 0}, XY{-100, -100}, 0)
	if math.Abs(east.Downwind-100) > tol || math.Abs(west.Downwind-100) > tol {
		t.Fatalf("对称两侧下风分量都应为 100，got %v,%v", east.Downwind, west.Downwind)
	}
	if math.Abs(east.Crosswind+west.Crosswind) > tol || east.Crosswind == 0 {
		t.Fatalf("两侧侧风分量应等大反号，got %v,%v", east.Crosswind, west.Crosswind)
	}
	// 源不在原点：位移按「受体−源」计算。
	shifted := Project(XY{50, 50}, XY{150, -50}, 0)
	if math.Abs(shifted.Downwind-100) > tol || math.Abs(shifted.Crosswind+100) > tol {
		t.Fatalf("源偏移后投影应等价: got (%v,%v)", shifted.Downwind, shifted.Crosswind)
	}
}

// 距离守恒：x²+y² 必须等于平面位移长度平方（纯旋转，无缩放）。
func TestProjectRotationPreservesDistance(t *testing.T) {
	src, rec := XY{12.5, -7.25}, XY{-88.0, 203.4}
	for _, wd := range []float64{-720, -90, 0, 45, 137.7, 270, 359.9, 765} {
		rel := Project(src, rec, wd)
		dx, dy := rec.X-src.X, rec.Y-src.Y
		want := dx*dx + dy*dy
		got := rel.Downwind*rel.Downwind + rel.Crosswind*rel.Crosswind
		if math.Abs(got-want) > 1e-9*math.Max(1, want) {
			t.Errorf("wind_dir=%v: 旋转不保距 %v != %v", wd, got, want)
		}
	}
}

func TestNormalizeWindDir(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0}, {360, 0}, {-90, 270}, {765, 45}, {180, 180},
	}
	for _, c := range cases {
		got, err := NormalizeWindDir(c.in)
		if err != nil || math.Abs(got-c.want) > tol {
			t.Errorf("NormalizeWindDir(%v)=(%v,%v), want %v", c.in, got, err, c.want)
		}
	}
	if _, err := NormalizeWindDir(math.NaN()); err == nil {
		t.Error("NaN 风向角应报错，不能参与投影")
	}
	// 等价角度（相差 360°）投影必须完全一致。
	a := Project(XY{0, 0}, XY{30, -40}, 30)
	b := Project(XY{0, 0}, XY{30, -40}, 390)
	if math.Abs(a.Downwind-b.Downwind) > tol || math.Abs(a.Crosswind-b.Crosswind) > tol {
		t.Errorf("相差 360° 的风向角投影必须一致: %+v vs %+v", a, b)
	}
}
