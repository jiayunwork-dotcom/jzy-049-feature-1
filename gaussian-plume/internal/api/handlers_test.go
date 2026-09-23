package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gaussian-plume/internal/job"
)

func newTestRouter() (*gin.Engine, *job.MemoryStore) {
	gin.SetMode(gin.TestMode)
	store := job.NewMemoryStore()
	svc := job.NewService(store)
	srv := NewServer(svc)
	r := gin.New()
	srv.Register(r)
	return r, store
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
