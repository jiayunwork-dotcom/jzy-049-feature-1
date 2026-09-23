// Package api 用 Gin 暴露 HTTP 能力：
//
//	POST /api/v1/calculate             单受体点浓度（返回 c、σy、σz、有效源高）
//	POST /api/v1/scans                 提交一条下风向扫描作业，落库，返回作业标识
//	GET  /api/v1/scans/:id             按标识回查历史扫描作业（输入+结果）
//	POST /api/v1/multisource-jobs      提交一次多点源稳态合成作业，落库，返回标识与逐受体结果
//	GET  /api/v1/multisource-jobs/:id  按标识回查多源作业（完整输入+逐源贡献拆解）
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
	"gaussian-plume/internal/multisource"
	"gaussian-plume/internal/plume"
	"gaussian-plume/internal/rise"
	"gaussian-plume/internal/validate"
)

// Server 持有作业服务（计算编排 + 持久化）。
type Server struct {
	jobs  *job.Service
	multi *multisource.Service
}

// NewServer 构造 HTTP 服务。
func NewServer(jobs *job.Service, multi *multisource.Service) *Server {
	return &Server{jobs: jobs, multi: multi}
}

// Register 把路由注册到给定 engine。
func (s *Server) Register(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	v1 := r.Group("/api/v1")
	{
		v1.POST("/calculate", s.calculate)
		v1.POST("/scans", s.submitScan)
		v1.GET("/scans/:id", s.getScan)
		v1.POST("/multisource-jobs", s.submitMultiSource)
		v1.GET("/multisource-jobs/:id", s.getMultiSource)
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

// submitMultiSource 提交一次多点源稳态合成作业并落库。
// 单个源/受体非法不拖垮整批：非法源按零贡献处理并说明原因（status=partial）。
func (s *Server) submitMultiSource(c *gin.Context) {
	var req model.MultiSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("请求体不是合法 JSON: "+err.Error(), nil))
		return
	}
	if err := multisource.ValidateJob(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("输入校验失败", err))
		return
	}
	rec, err := s.multi.Submit(c.Request.Context(), req, uuid.NewString())
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("作业提交失败: "+err.Error(), nil))
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"job_id":     rec.ID,
		"status":     rec.Result.Status,
		"receptors":  rec.Result.Receptors,
		"created_at": rec.CreatedAt,
	})
}

// getMultiSource 按作业标识回查多源合成作业（完整输入 + 逐源贡献拆解）。
func (s *Server) getMultiSource(c *gin.Context) {
	id := c.Param("id")
	rec, err := s.multi.Get(c.Request.Context(), id)
	var nf multisource.ErrNotFound
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
