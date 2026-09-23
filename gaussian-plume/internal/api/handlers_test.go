package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gaussian-plume/internal/job"
	"gaussian-plume/internal/multijob"
)

func newTestRouter() (*gin.Engine, *job.MemoryStore) {
	gin.SetMode(gin.TestMode)
	store := job.NewMemoryStore()
	svc := job.NewService(store)
	multiStore := multijob.NewMemoryStore()
	multiSvc := multijob.NewService(multiStore)
	srv := NewServer(svc).WithMulti(multiSvc)
	r := gin.New()
	srv.Register(r)
	return r, store
}

// newTestRouters 同时返回单源/多源两个内存存储，供多源接口测试回查。
func newTestRouters() (*gin.Engine, *job.MemoryStore, *multijob.MemoryStore) {
	gin.SetMode(gin.TestMode)
	store := job.NewMemoryStore()
	multiStore := multijob.NewMemoryStore()
	srv := NewServer(job.NewService(store)).WithMulti(multijob.NewService(multiStore))
	r := gin.New()
	srv.Register(r)
	return r, store, multiStore
}

func doJSON(t *testing.T, r http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCalculateEndpoint(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodPost, "/api/v1/calculate", map[string]interface{}{
		"q": 0.1, "physical_h": 0, "u": 5, "stability": "D", "x": 1000,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("状态码=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]float64
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"c", "sigma_y", "sigma_z", "effective_h"} {
		if _, ok := resp[k]; !ok {
			t.Errorf("响应缺少 %s", k)
		}
	}
	if resp["c"] <= 0 {
		t.Error("浓度应为正")
	}
}

func TestCalculateEndpointBadInput(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodPost, "/api/v1/calculate", map[string]interface{}{
		"q": -1, "u": 0, "stability": "Z", "x": 0,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法输入应 400，got %d", w.Code)
	}
	var resp struct {
		Error struct {
			Fields []struct{ Field string } `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Error.Fields) == 0 {
		t.Error("应返回结构化字段错误")
	}
}

func TestScanSubmitAndGet(t *testing.T) {
	r, store := newTestRouter()
	w := doJSON(t, r, http.MethodPost, "/api/v1/scans", map[string]interface{}{
		"q": 0.1, "physical_h": 0, "u": 5, "stability": "D",
		"distances": []float64{100, -1, 1000},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("提交应 201，got %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
		Points []struct {
			Error string `json:"error"`
		} `json:"points"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.JobID == "" {
		t.Fatal("应返回作业标识")
	}
	if created.Status != "partial" {
		t.Errorf("含坏点应 partial，got %s", created.Status)
	}
	if created.Points[1].Error == "" {
		t.Error("非法点应带错误说明")
	}
	if store.Len() != 1 {
		t.Errorf("应落库 1 条，got %d", store.Len())
	}

	// 回查。
	wg := doJSON(t, r, http.MethodGet, "/api/v1/scans/"+created.JobID, nil)
	if wg.Code != http.StatusOK {
		t.Fatalf("回查应 200，got %d", wg.Code)
	}
	var got struct {
		Request map[string]interface{} `json:"request"`
		Result  struct {
			Points []json.RawMessage `json:"points"`
		} `json:"result"`
	}
	if err := json.Unmarshal(wg.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Request["q"] == nil || len(got.Result.Points) != 3 {
		t.Error("回查内容应含完整输入与 3 个结果点")
	}
}

func TestGetScanNotFound(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodGet, "/api/v1/scans/does-not-exist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在作业应 404，got %d", w.Code)
	}
}

func TestHealthz(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodGet, "/healthz", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz 应 200，got %d", w.Code)
	}
}

func twoSourceBody(overrides map[string]interface{}) map[string]interface{} {
	body := map[string]interface{}{
		"wind_dir":  0.0, // 北风（来向），烟羽向南
		"u":         5.0,
		"stability": "D",
		"receptors": []map[string]interface{}{
			{"id": "R", "x": 0.0, "y": -1000.0},
		},
		"sources": []map[string]interface{}{
			{"id": "A", "x": 0.0, "y": 0.0, "q": 0.1, "physical_h": 30.0},
			{"id": "B", "x": 100.0, "y": 0.0, "q": 0.05, "physical_h": 60.0},
		},
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

func TestMultiSourceSubmitAndGet(t *testing.T) {
	r, _, multiStore := newTestRouters()
	w := doJSON(t, r, http.MethodPost, "/api/v1/multi-source-jobs", twoSourceBody(nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("提交应 201，got %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
		Recs   []struct {
			C             float64 `json:"c"`
			Contributions []struct {
				SourceID string  `json:"source_id"`
				C        float64 `json:"c"`
				Share    float64 `json:"share"`
			} `json:"contributions"`
		} `json:"receptors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.JobID == "" {
		t.Fatal("应返回作业标识")
	}
	if created.Status != "ok" {
		t.Errorf("合法批应 ok，got %s", created.Status)
	}
	if len(created.Recs) != 1 || len(created.Recs[0].Contributions) != 2 {
		t.Fatalf("应返回 1 受体 ×2 源贡献")
	}
	sumShare := created.Recs[0].Contributions[0].Share + created.Recs[0].Contributions[1].Share
	if math.Abs(sumShare-1) > 1e-12 {
		t.Errorf("占比之和应为 1，got %v", sumShare)
	}
	sumC := created.Recs[0].Contributions[0].C + created.Recs[0].Contributions[1].C
	if math.Abs(sumC-created.Recs[0].C) > 1e-15 {
		t.Errorf("逐项贡献之和应等于合成浓度")
	}
	if multiStore.Len() != 1 {
		t.Errorf("应落库 1 条多源作业，got %d", multiStore.Len())
	}

	// 回查：完整源输入 + 逐项贡献都能还原。
	wg := doJSON(t, r, http.MethodGet, "/api/v1/multi-source-jobs/"+created.JobID, nil)
	if wg.Code != http.StatusOK {
		t.Fatalf("回查应 200，got %d body=%s", wg.Code, wg.Body.String())
	}
	var got struct {
		Request struct {
			WindDir float64 `json:"wind_dir"`
			Sources []struct {
				ID        string  `json:"id"`
				Q         float64 `json:"q"`
				PhysicalH float64 `json:"physical_h"`
			} `json:"sources"`
		} `json:"request"`
		Result struct {
			Receptors []json.RawMessage `json:"receptors"`
		} `json:"result"`
	}
	if err := json.Unmarshal(wg.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Request.WindDir != 0 || len(got.Request.Sources) != 2 ||
		got.Request.Sources[0].Q != 0.1 || got.Request.Sources[1].PhysicalH != 60 {
		t.Errorf("回查应还原每个源的原始输入，got %+v", got.Request)
	}
	if len(got.Result.Receptors) != 1 {
		t.Errorf("回查结果受体数不对")
	}
}

// 批量中单个源非法：HTTP 仍 201（不整批报错），该源零贡献带 error，其余照算。
func TestMultiSourceBadSourceDoesNotFailBatch(t *testing.T) {
	r, _, _ := newTestRouters()
	body := twoSourceBody(nil)
	srcs := body["sources"].([]map[string]interface{})
	srcs[1]["q"] = -0.05 // 源 B 非法
	w := doJSON(t, r, http.MethodPost, "/api/v1/multi-source-jobs", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("单源非法不应整批失败，got %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Status string `json:"status"`
		Recs   []struct {
			Contributions []struct {
				SourceID string  `json:"source_id"`
				C        float64 `json:"c"`
				Error    string  `json:"error"`
			} `json:"contributions"`
		} `json:"receptors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "partial" {
		t.Errorf("应 partial，got %s", created.Status)
	}
	cs := created.Recs[0].Contributions
	if cs[0].C <= 0 || cs[0].Error != "" {
		t.Errorf("合法源应照算，got %+v", cs[0])
	}
	if cs[1].C != 0 || cs[1].Error == "" {
		t.Errorf("非法源应零贡献带原因，got %+v", cs[1])
	}
}

// 作业级公共条件非法：整单 400 结构化错误，不落库。
func TestMultiSourceJobLevelBadRequest(t *testing.T) {
	r, _, multiStore := newTestRouters()
	for _, ov := range []map[string]interface{}{
		{"u": 0.0},
		{"stability": "Z"},
		{"wind_dir": "not-a-number"},
		{"sources": []interface{}{}},
		{"receptors": []interface{}{}},
	} {
		w := doJSON(t, r, http.MethodPost, "/api/v1/multi-source-jobs", twoSourceBody(ov))
		if w.Code != http.StatusBadRequest {
			t.Errorf("作业级非法 %v 应 400，got %d body=%s", ov, w.Code, w.Body.String())
		}
	}
	if multiStore.Len() != 0 {
		t.Errorf("作业级非法不得落库，got %d", multiStore.Len())
	}
}

func TestGetMultiJobNotFound(t *testing.T) {
	r, _, _ := newTestRouters()
	w := doJSON(t, r, http.MethodGet, "/api/v1/multi-source-jobs/does-not-exist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在多源作业应 404，got %d", w.Code)
	}
}

// 旧的单源接口行为不变（新一层挂上后）。
func TestLegacyEndpointsUnchanged(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodPost, "/api/v1/calculate", map[string]interface{}{
		"q": 0.1, "physical_h": 0, "u": 5, "stability": "D", "x": 1000,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("单源计算应仍 200，got %d", w.Code)
	}
}
