package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"gaussian-plume/internal/model"
	"gaussian-plume/internal/multijob"
)

// SaveMulti 写入一条多源叠加作业：主行 + 逐源明细行在同一事务里提交。
// 每个作业用自己的 UUID 作主键，ON CONFLICT DO NOTHING；并发提交的多个作业
// 写各自的行，事务 + 主键唯一约束保证互不串写、不互相覆盖。
func (p *Postgres) SaveMulti(ctx context.Context, rec model.MultiSourceJobRecord) error {
	reqJSON, err := json.Marshal(rec.Request)
	if err != nil {
		return fmt.Errorf("序列化多源 request 失败: %w", err)
	}
	resJSON, err := json.Marshal(rec.Result)
	if err != nil {
		return fmt.Errorf("序列化多源 result 失败: %w", err)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启多源作业事务失败: %w", err)
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		INSERT INTO multi_source_jobs (id, wind_dir, u, stability, status, request, result, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING`,
		rec.ID,
		rec.Request.WindDir,
		rec.Request.U,
		string(rec.Request.Stability),
		rec.Result.Status,
		string(reqJSON),
		string(resJSON),
		rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("写入多源作业主行失败: %w", err)
	}
	// 固定 id 撞车（幂等预置场景）：主行未插入则明细也不重复写。
	if ct.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	for i, src := range rec.Request.Sources {
		srcJSON, err := json.Marshal(src)
		if err != nil {
			return fmt.Errorf("序列化源明细失败: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO multi_source_job_sources
			    (job_id, source_index, source_id, sx, sy, q, physical_h, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT DO NOTHING`,
			rec.ID, i, src.ID, src.X, src.Y, src.Q, src.PhysicalH, string(srcJSON),
		); err != nil {
			return fmt.Errorf("写入多源作业源明细[%d] 失败: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交多源作业事务失败: %w", err)
	}
	return nil
}

// GetMulti 按标识回查多源作业；主行不存在返回 multijob.ErrNotFound。
// 完整请求（含每个源输入）与叠加结果从主行 JSONB 原样还原；另用源明细表
// 交叉核对源数量，确保回查得到的不只是总浓度数字。
func (p *Postgres) GetMulti(ctx context.Context, id string) (model.MultiSourceJobRecord, error) {
	var (
		rec     model.MultiSourceJobRecord
		reqJSON string
		resJSON string
	)
	err := p.pool.QueryRow(ctx, `
		SELECT id, request, result,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM multi_source_jobs WHERE id = $1`, id).
		Scan(&rec.ID, &reqJSON, &resJSON, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.MultiSourceJobRecord{}, multijob.ErrNotFound{ID: id}
	}
	if err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("读取多源作业失败: %w", err)
	}
	if err := json.Unmarshal([]byte(reqJSON), &rec.Request); err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("反序列化多源 request 失败: %w", err)
	}
	if err := json.Unmarshal([]byte(resJSON), &rec.Result); err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("反序列化多源 result 失败: %w", err)
	}

	rows, err := p.pool.Query(ctx, `
		SELECT source_index, source_id, sx, sy, q, physical_h, source
		FROM multi_source_job_sources WHERE job_id = $1
		ORDER BY source_index`, id)
	if err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("读取多源作业源明细失败: %w", err)
	}
	defer rows.Close()
	var nDetail int
	for rows.Next() {
		var (
			idx    int
			sid    string
			sx, sy float64
			q, ph  float64
			sj     string
		)
		if err := rows.Scan(&idx, &sid, &sx, &sy, &q, &ph, &sj); err != nil {
			return model.MultiSourceJobRecord{}, fmt.Errorf("扫描源明细行失败: %w", err)
		}
		var src model.PointSourceInput
		if err := json.Unmarshal([]byte(sj), &src); err != nil {
			return model.MultiSourceJobRecord{}, fmt.Errorf("反序列化源明细行失败: %w", err)
		}
		if idx != nDetail || sid != src.ID || sx != src.X || sy != src.Y ||
			q != src.Q || ph != src.PhysicalH {
			return model.MultiSourceJobRecord{}, fmt.Errorf(
				"源明细[%d] 标量列与 JSON 不一致（源回查不完整）", idx)
		}
		nDetail++
	}
	if err := rows.Err(); err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("遍历源明细失败: %w", err)
	}
	if nDetail != len(rec.Request.Sources) {
		return model.MultiSourceJobRecord{}, fmt.Errorf(
			"源明细行数 %d 与请求源数 %d 不一致", nDetail, len(rec.Request.Sources))
	}
	return rec, nil
}
