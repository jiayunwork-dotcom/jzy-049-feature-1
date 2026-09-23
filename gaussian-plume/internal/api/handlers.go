// Package api 用 Gin 暴露这些 HTTP 能力：
//
//	POST /api/v1/calculate                单受体点浓度（返回 c、σy、σz、有效源高）
//	POST /api/v1/scans                    提交一条下风向扫描作业，落库，返回作业标识
//	GET  /api/v1/scans/:id                按标识回查历史单源扫描作业（输入+结果）
//	POST /api/v1/multi-source-jobs        提交多点源叠加作业，落库，返回作业标识
//	GET  /api/v1/multi-source-jobs/:id    按标识回查多源作业（完整源输入+逐项贡献）
//
// 多源能力是叠在原有单源接口之上的一层：单源计算与扫描两个接口原样保留。
//
// 另：GET /healthz 存活探针。
package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gaussian-plume/internal/job"
	"gaussian-plume/internal/model"
	"gaussian-plume/internal/multijob"
	"gaussian-plume/internal/plume"
	"gaussian-plume/internal/rise"
	"gaussian-plume/internal/validate"
)

// Server 持有作业服务（计算编排 + 持久化）。multiJobs 为可选的多源叠加服务；
// 为 nil 时不注册多源路由（单源行为一点不变）。
type Server struct {
	jobs      *job.Service
	multiJobs *multijob.Service
}

// NewServer 构造 HTTP 服务（仅单源能力，行为与历史完全一致）。
func NewServer(jobs *job.Service) *Server {
	return &Server{jobs: jobs}
}

// WithMulti 挂上多源叠加服务并返回同一 Server（链式启用新一层能力）。
func (s *Server) WithMulti(m *multijob.Service) *Server {
	s.multiJobs = m
	return s
}

// Register 把路由注册到给定 engine。
func (s *Server) Register(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	v1 := r.Group("/api/v1")
	{
		v1.POST("/calculate", s.calculate)
		v1.POST("/scans", s.submitScan)
		v1.GET("/scans/:id", s.getScan)
		if s.multiJobs != nil {
			v1.POST("/multi-source-jobs", s.submitMulti)
			v1.GET("/multi-source-jobs/:id", s.getMulti)
		}
	}
}

// errorBody 输出结构化错误说明。
func errorBody(msg string, fields interface{}) gin.H {
	h := gin.H{"error": gin.H{"message": msg}}
	if fields != nil {
		h["error"].(gin.H)["fields"] = fields
	}
	return h
}

// calculate 处理单受体点计算。
func (s *Server) calculate(c *gin.Context) {
	var req validate.SingleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("请求体不是合法 JSON: "+err.Error(), nil))
		return
	}
	if err := validate.Single(req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("输入校验失败", err))
		return
	}
	req.Stability = req.Stability.Normalize()

	h := req.EffectiveH
	var dh float64
	var regime string
	if req.BuoyancyRise != nil {
		in := *req.BuoyancyRise
		in.Stability = req.Stability
		if in.U == 0 {
			in.U = req.U
		}
		in.X = req.X // 按受体点下风距离截断抬升
		hh, d, _, rg, err := rise.EffectiveHeight(req.PhysicalH, in)
		if err != nil {
			c.JSON(http.StatusBadRequest, errorBody("抬升计算失败: "+err.Error(), nil))
			return
		}
		h, dh, regime = hh, d, rg
	} else if !req.UseGivenH {
		h = req.PhysicalH
	}

	out, err := plume.Calculate(model.PlumeParams{
		Q:          req.Q,
		EffectiveH: h,
		U:          req.U,
		Stability:  req.Stability,
		X:          req.X,
		Y:          req.Y,
		Z:          req.Z,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("浓度计算失败: "+err.Error(), nil))
		return
	}
	resp := gin.H{
		"c":           out.C,
		"sigma_y":     out.SigmaY,
		"sigma_z":     out.SigmaZ,
		"effective_h": out.EffectiveH,
	}
	if req.BuoyancyRise != nil {
		resp["delta_h"] = dh
		resp["rise_regime"] = regime
	}
	c.JSON(http.StatusOK, resp)
}

// scanRequestAPI 是扫描接口的 JSON 入参（点位只要求 x，y/z 可选）。
type scanRequestAPI struct {
	Q            float64          `json:"q"`
	PhysicalH    float64          `json:"physical_h"`
	U            float64          `json:"u"`
	Stability    model.Stability  `json:"stability"`
	Distances    []float64        `json:"distances"`
	Y            float64          `json:"y"`
	Z            float64          `json:"z"`
	EffectiveH   float64          `json:"effective_h,omitempty"`
	UseGivenH    bool             `json:"use_given_h,omitempty"`
	BuoyancyRise *model.RiseInput `json:"buoyancy_rise,omitempty"`
}

func (a scanRequestAPI) toModel() model.ScanRequest {
	pts := make([]model.ScanPointInput, 0, len(a.Distances))
	for _, x := range a.Distances {
		pts = append(pts, model.ScanPointInput{X: x, Y: a.Y, Z: a.Z})
	}
	return model.ScanRequest{
		Q:            a.Q,
		PhysicalH:    a.PhysicalH,
		U:            a.U,
		Stability:    a.Stability,
		Points:       pts,
		BuoyancyRise: a.BuoyancyRise,
		EffectiveH:   a.EffectiveH,
		UseGivenH:    a.UseGivenH,
	}
}

// submitScan 提交一条扫描作业并落库。
func (s *Server) submitScan(c *gin.Context) {
	var in scanRequestAPI
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("请求体不是合法 JSON: "+err.Error(), nil))
		return
	}
	req := in.toModel()
	if err := validate.Scan(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("输入校验失败", err))
		return
	}
	rec, err := s.jobs.Submit(c.Request.Context(), req, uuid.NewString())
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("作业提交失败: "+err.Error(), nil))
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"job_id":      rec.ID,
		"status":      rec.Result.Status,
		"effective_h": rec.Result.EffectiveH,
		"delta_h":     rec.Result.DeltaH,
		"points":      rec.Result.Points,
		"created_at":  rec.CreatedAt,
	})
}

// xyAPI 是 JSON 里的水平坐标（东向 x、北向 y，m）。
type xyAPI struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// getScan 按作业标识回查。
func (s *Server) getScan(c *gin.Context) {
	id := c.Param("id")
	rec, err := s.jobs.Get(c.Request.Context(), id)
	var nf job.ErrNotFound
	if errors.As(err, &nf) {
		c.JSON(http.StatusNotFound, errorBody(err.Error(), nil))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("回查失败: "+err.Error(), nil))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"job_id":     rec.ID,
		"created_at": rec.CreatedAt,
		"request":    rec.Request,
		"result":     rec.Result,
	})
}

// sourceAPI 是多源请求里的一个点源：位置 + 源强 + 物理源高，热抬升可选。
type sourceAPI struct {
	ID           string           `json:"id,omitempty"`
	X            float64          `json:"x"`
	Y            float64          `json:"y"`
	Q            float64          `json:"q"`
	PhysicalH    float64          `json:"physical_h"`
	BuoyancyRise *model.RiseInput `json:"buoyancy_rise,omitempty"`
	EffectiveH   float64          `json:"effective_h,omitempty"`
	UseGivenH    bool             `json:"use_given_h,omitempty"`
}

// multiRequestAPI 是多源叠加接口的 JSON 入参。
//
// wind_dir 全服务钉死一种约定：气象罗盘风向角（度），正北起算、顺时针，
// 表示风的【来向】（0=北风、90=东风、180=南风、270=西风）。
type multiRequestAPI struct {
	WindDir   float64         `json:"wind_dir"`
	U         float64         `json:"u"`
	Stability model.Stability `json:"stability"`
	Receptors []struct {
		ID string `json:"id,omitempty"`
		xyAPI
	} `json:"receptors"`
	Sources []sourceAPI `json:"sources"`
}

func (a multiRequestAPI) toModel() model.MultiSourceRequest {
	recs := make([]model.ReceptorInput, 0, len(a.Receptors))
	for _, r := range a.Receptors {
		recs = append(recs, model.ReceptorInput{ID: r.ID, XY: model.XY{X: r.X, Y: r.Y}})
	}
	srcs := make([]model.PointSourceInput, 0, len(a.Sources))
	for _, s := range a.Sources {
		srcs = append(srcs, model.PointSourceInput{
			ID:           s.ID,
			XY:           model.XY{X: s.X, Y: s.Y},
			Q:            s.Q,
			PhysicalH:    s.PhysicalH,
			BuoyancyRise: s.BuoyancyRise,
			EffectiveH:   s.EffectiveH,
			UseGivenH:    s.UseGivenH,
		})
	}
	return model.MultiSourceRequest{
		WindDir:   a.WindDir,
		U:         a.U,
		Stability: a.Stability,
		Receptors: recs,
		Sources:   srcs,
	}
}

// submitMulti 提交一次多点源叠加作业并落库。
func (s *Server) submitMulti(c *gin.Context) {
	var in multiRequestAPI
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("请求体不是合法 JSON: "+err.Error(), nil))
		return
	}
	req := in.toModel()
	rec, err := s.multiJobs.Submit(c.Request.Context(), req, uuid.NewString())
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("输入校验失败", err))
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"job_id":     rec.ID,
		"status":     rec.Result.Status,
		"wind_dir":   rec.Result.WindDir,
		"u":          rec.Result.U,
		"stability":  rec.Result.Stability,
		"receptors":  rec.Result.Receptors,
		"created_at": rec.CreatedAt,
	})
}

// getMulti 按作业标识回查多源作业（完整源输入 + 逐项贡献/占比）。
func (s *Server) getMulti(c *gin.Context) {
	id := c.Param("id")
	rec, err := s.multiJobs.Get(c.Request.Context(), id)
	var nf multijob.ErrNotFound
	if errors.As(err, &nf) {
		c.JSON(http.StatusNotFound, errorBody(err.Error(), nil))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("回查失败: "+err.Error(), nil))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"job_id":     rec.ID,
		"created_at": rec.CreatedAt,
		"request":    rec.Request,
		"result":     rec.Result,
	})
}
