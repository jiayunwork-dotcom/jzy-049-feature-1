package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

// 多源作业：提交 -> 201 + 逐受体合成与逐源拆解；回查 -> 完整输入与结果还原。
func TestMultiSourceSubmitAndGet(t *testing.T) {
	r, _ := newTestRouter()
	body := map[string]interface{}{
		"wind_angle_deg": 0, "u": 5, "stability": "D",
		"sources": []map[string]interface{}{
			{"name": "1号炉", "x": 0, "y": 0, "q": 0.1, "physical_h": 60},
			{"name": "2号炉", "x": 0, "y": 150, "q": 0.05, "physical_h": 40},
		},
		"receptors": []map[string]interface{}{
			{"name": "厂界东", "x": 1000, "y": 0},
			{"name": "最近居民点", "x": 1200, "y": 300},
		},
	}
	w := doJSON(t, r, http.MethodPost, "/api/v1/multisource-jobs", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("提交应 201，got %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		JobID     string `json:"job_id"`
		Status    string `json:"status"`
		Receptors []struct {
			TotalC        float64 `json:"total_c"`
			Contributions []struct {
				SourceName string  `json:"source_name"`
				C          float64 `json:"c"`
				Share      float64 `json:"share"`
				Status     string  `json:"status"`
			} `json:"contributions"`
		} `json:"receptors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.JobID == "" || created.Status != "ok" {
		t.Fatalf("响应缺 job_id/status: %s", w.Body.String())
	}
	if len(created.Receptors) != 2 {
		t.Fatalf("应返回 2 个受体，got %d", len(created.Receptors))
	}
	for i, rec := range created.Receptors {
		if len(rec.Contributions) != 2 {
			t.Fatalf("受体%d 应有 2 条逐源拆解", i)
		}
		sum := 0.0
		shareSum := 0.0
		for _, c := range rec.Contributions {
			sum += c.C
			shareSum += c.Share
			if c.Status != "ok" {
				t.Errorf("受体%d 贡献状态异常: %+v", i, c)
			}
		}
		if math.Abs(rec.TotalC-sum) > 1e-18*math.Max(1, sum) {
			t.Errorf("受体%d: total_c %v != 逐源和 %v", i, rec.TotalC, sum)
		}
		if math.Abs(shareSum-1) > 1e-9 {
			t.Errorf("受体%d: 占比和 %v 应为 1", i, shareSum)
		}
	}

	// 回查：完整输入（每个源的坐标/排放条件）与结果都要在。
	wg := doJSON(t, r, http.MethodGet, "/api/v1/multisource-jobs/"+created.JobID, nil)
	if wg.Code != http.StatusOK {
		t.Fatalf("回查应 200，got %d", wg.Code)
	}
	var got struct {
		Request struct {
			WindAngleDeg float64 `json:"wind_angle_deg"`
			Sources      []struct {
				Name      string  `json:"name"`
				X         float64 `json:"x"`
				Y         float64 `json:"y"`
				Q         float64 `json:"q"`
				PhysicalH float64 `json:"physical_h"`
			} `json:"sources"`
			Receptors []map[string]interface{} `json:"receptors"`
		} `json:"request"`
		Result struct {
			Receptors []json.RawMessage `json:"receptors"`
		} `json:"result"`
	}
	if err := json.Unmarshal(wg.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Request.WindAngleDeg != 0 || len(got.Request.Sources) != 2 || len(got.Request.Receptors) != 2 {
		t.Fatalf("回查输入不完整: %s", wg.Body.String())
	}
	s1 := got.Request.Sources[1]
	if s1.Name != "2号炉" || s1.Y != 150 || s1.Q != 0.05 || s1.PhysicalH != 40 {
		t.Errorf("回查的源输入被串改: %+v", s1)
	}
	if len(got.Result.Receptors) != 2 {
		t.Errorf("回查结果应含 2 个受体")
	}
}

// 批量里夹一个非法源：整批照常 201，坏源零贡献并说明原因，好源合成不受影响。
func TestMultiSourceInvalidSourceIsolated(t *testing.T) {
	r, _ := newTestRouter()
	mk := func(sources ...map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"wind_angle_deg": 0, "u": 5, "stability": "D",
			"sources":   sources,
			"receptors": []map[string]interface{}{{"x": 1000, "y": 0}},
		}
	}
	good1 := map[string]interface{}{"x": 0, "y": 0, "q": 0.1, "physical_h": 60}
	good2 := map[string]interface{}{"x": 100, "y": 0, "q": 0.05, "physical_h": 40}
	bad := map[string]interface{}{"x": 50, "y": 0, "q": -1, "physical_h": 60}

	wGood := doJSON(t, r, http.MethodPost, "/api/v1/multisource-jobs", mk(good1, good2))
	wMix := doJSON(t, r, http.MethodPost, "/api/v1/multisource-jobs", mk(good1, bad, good2))
	if wGood.Code != http.StatusCreated || wMix.Code != http.StatusCreated {
		t.Fatalf("两批都应 201: %d / %d", wGood.Code, wMix.Code)
	}
	var g, m struct {
		Status    string `json:"status"`
		Receptors []struct {
			TotalC        float64 `json:"total_c"`
			Contributions []struct {
				C      float64 `json:"c"`
				Share  float64 `json:"share"`
				Status string  `json:"status"`
				Reason string  `json:"reason"`
			} `json:"contributions"`
		} `json:"receptors"`
	}
	if err := json.Unmarshal(wGood.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wMix.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Status != "partial" {
		t.Errorf("含坏源应 partial，got %s", m.Status)
	}
	if m.Receptors[0].TotalC != g.Receptors[0].TotalC {
		t.Errorf("坏源不应影响合成: %v != %v", m.Receptors[0].TotalC, g.Receptors[0].TotalC)
	}
	badC := m.Receptors[0].Contributions[1]
	if badC.Status != "invalid" || badC.C != 0 || badC.Share != 0 || badC.Reason == "" {
		t.Errorf("坏源拆解异常: %+v", badC)
	}
}

// 作业级非法（风速非正、源批为空等）-> 400 + 结构化字段错误，不落库。
func TestMultiSourceJobLevelInvalid(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodPost, "/api/v1/multisource-jobs", map[string]interface{}{
		"wind_angle_deg": 0, "u": 0, "stability": "D",
		"sources":   []map[string]interface{}{{"x": 0, "y": 0, "q": 0.1, "physical_h": 60}},
		"receptors": []map[string]interface{}{{"x": 1000, "y": 0}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("风速非正应 400，got %d", w.Code)
	}
	w = doJSON(t, r, http.MethodPost, "/api/v1/multisource-jobs", map[string]interface{}{
		"wind_angle_deg": 0, "u": 5, "stability": "D",
		"sources": []map[string]interface{}{}, "receptors": []map[string]interface{}{{"x": 1, "y": 1}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("空源批应 400，got %d", w.Code)
	}
}

// 回查不存在的多源作业 -> 404。
func TestGetMultiSourceNotFound(t *testing.T) {
	r, _ := newTestRouter()
	w := doJSON(t, r, http.MethodGet, "/api/v1/multisource-jobs/does-not-exist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在作业应 404，got %d", w.Code)
	}
}
