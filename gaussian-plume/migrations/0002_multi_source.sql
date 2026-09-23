-- 0002：多点源叠加作业（多源作业主表 + 逐源输入明细表）。
-- 与 internal/store/migrations_schema.sql 中对应段落保持同一份定义。

CREATE TABLE IF NOT EXISTS multi_source_jobs (
    id           TEXT PRIMARY KEY,
    wind_dir     DOUBLE PRECISION NOT NULL,
    u            DOUBLE PRECISION NOT NULL,
    stability    CHAR(1)          NOT NULL,
    status       TEXT             NOT NULL,
    request      JSONB            NOT NULL,
    result       JSONB            NOT NULL,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_multi_source_jobs_created_at ON multi_source_jobs (created_at DESC);

CREATE TABLE IF NOT EXISTS multi_source_job_sources (
    job_id       TEXT NOT NULL REFERENCES multi_source_jobs(id) ON DELETE CASCADE,
    source_index INTEGER NOT NULL,
    source_id    TEXT NOT NULL,
    sx           DOUBLE PRECISION NOT NULL,
    sy           DOUBLE PRECISION NOT NULL,
    q            DOUBLE PRECISION NOT NULL,
    physical_h   DOUBLE PRECISION NOT NULL,
    source       JSONB NOT NULL,
    PRIMARY KEY (job_id, source_index)
);

CREATE INDEX IF NOT EXISTS idx_multi_source_job_sources_job ON multi_source_job_sources (job_id);
