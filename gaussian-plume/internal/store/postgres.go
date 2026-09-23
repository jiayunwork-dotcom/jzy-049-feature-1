// Package store 提供扫描作业的 PostgreSQL 16 持久化。
//
// 并发安全：每次提交都是独立行的 INSERT（主键为各自作业 UUID），
// 不同作业之间没有共享可变状态，数据库行锁/唯一约束保证不会写串、
// 不会互相覆盖。进程重启后数据仍在 PostgreSQL 中，可照常回查。
package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gaussian-plume/internal/job"
	"gaussian-plume/internal/model"
	"gaussian-plume/internal/multisource"
)

//go:embed migrations_schema.sql
var schemaSQL string

// Postgres 是 job.Store 的 PostgreSQL 实现。
type Postgres struct {
	pool *pgxpool.Pool
}

// New 建立连接池并确保 schema 已应用（幂等迁移）。
func New(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析数据库 DSN 失败: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("建立连接池失败: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	pg := &Postgres{pool: pool}
	if err := pg.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pg, nil
}

func (p *Postgres) migrate(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("应用迁移失败: %w", err)
	}
	return nil
}

// Close 关闭连接池。
func (p *Postgres) Close() { p.pool.Close() }

// Save 插入一条扫描作业行。
func (p *Postgres) Save(ctx context.Context, rec model.JobRecord) error {
	reqJSON, err := json.Marshal(rec.Request)
	if err != nil {
		return fmt.Errorf("序列化 request 失败: %w", err)
	}
	resJSON, err := json.Marshal(rec.Result)
	if err != nil {
		return fmt.Errorf("序列化 result 失败: %w", err)
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO scan_jobs (id, q, physical_h, u, stability, status, request, result, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`,
		rec.ID,
		rec.Request.Q,
		rec.Request.PhysicalH,
		rec.Request.U,
		string(rec.Request.Stability),
		rec.Result.Status,
		string(reqJSON),
		string(resJSON),
		rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("写入扫描作业失败: %w", err)
	}
	return nil
}

// Get 按标识回查作业；不存在返回 job.ErrNotFound。
func (p *Postgres) Get(ctx context.Context, id string) (model.JobRecord, error) {
	var (
		rec     model.JobRecord
		reqJSON string
		resJSON string
		stab    string
	)
	err := p.pool.QueryRow(ctx, `
		SELECT id, request, result, stability, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM scan_jobs WHERE id = $1`, id).
		Scan(&rec.ID, &reqJSON, &resJSON, &stab, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.JobRecord{}, job.ErrNotFound{ID: id}
	}
	if err != nil {
		return model.JobRecord{}, fmt.Errorf("读取扫描作业失败: %w", err)
	}
	if err := json.Unmarshal([]byte(reqJSON), &rec.Request); err != nil {
		return model.JobRecord{}, fmt.Errorf("反序列化 request 失败: %w", err)
	}
	if err := json.Unmarshal([]byte(resJSON), &rec.Result); err != nil {
		return model.JobRecord{}, fmt.Errorf("反序列化 result 失败: %w", err)
	}
	return rec, nil
}

// SaveMulti 插入一条多源合成作业行（multisource.Store 实现）。
// 完整输入（每个源的坐标/排放条件）与逐受体、逐源拆解结果整体存 JSONB，
// 回查时可全部还原，不只是一个总浓度数字。
func (p *Postgres) SaveMulti(ctx context.Context, rec model.MultiSourceJobRecord) error {
	reqJSON, err := json.Marshal(rec.Request)
	if err != nil {
		return fmt.Errorf("序列化 request 失败: %w", err)
	}
	resJSON, err := json.Marshal(rec.Result)
	if err != nil {
		return fmt.Errorf("序列化 result 失败: %w", err)
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO multisource_jobs (id, wind_angle_deg, u, stability, status, request, result, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING`,
		rec.ID,
		rec.Request.WindAngleDeg,
		rec.Request.U,
		string(rec.Request.Stability),
		rec.Result.Status,
		string(reqJSON),
		string(resJSON),
		rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("写入多源合成作业失败: %w", err)
	}
	return nil
}

// GetMulti 按标识回查多源合成作业；不存在返回 multisource.ErrNotFound。
func (p *Postgres) GetMulti(ctx context.Context, id string) (model.MultiSourceJobRecord, error) {
	var (
		rec     model.MultiSourceJobRecord
		reqJSON string
		resJSON string
		stab    string
	)
	err := p.pool.QueryRow(ctx, `
		SELECT id, request, result, stability, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM multisource_jobs WHERE id = $1`, id).
		Scan(&rec.ID, &reqJSON, &resJSON, &stab, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.MultiSourceJobRecord{}, multisource.ErrNotFound{ID: id}
	}
	if err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("读取多源合成作业失败: %w", err)
	}
	if err := json.Unmarshal([]byte(reqJSON), &rec.Request); err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("反序列化 request 失败: %w", err)
	}
	if err := json.Unmarshal([]byte(resJSON), &rec.Result); err != nil {
		return model.MultiSourceJobRecord{}, fmt.Errorf("反序列化 result 失败: %w", err)
	}
	return rec, nil
}
