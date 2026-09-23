-- 多源合成作业表：每行是一条可回查的多点源稳态合成作业。
-- 与 store 内嵌的 migrations_schema.sql 同一份（启动幂等应用）。
CREATE TABLE IF NOT EXISTS multisource_jobs (
    id             TEXT PRIMARY KEY,
    wind_angle_deg DOUBLE PRECISION NOT NULL,
    u              DOUBLE PRECISION NOT NULL,
    stability      CHAR(1)          NOT NULL,
    status         TEXT             NOT NULL,
    request        JSONB            NOT NULL,
    result         JSONB            NOT NULL,
    created_at     TIMESTAMPTZ      NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_multisource_jobs_created_at ON multisource_jobs (created_at DESC);
