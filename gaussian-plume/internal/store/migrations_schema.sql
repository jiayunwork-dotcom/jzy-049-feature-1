-- 扫描作业表：每行是一条可回查的下风向扫描作业。
-- 作业输入与结果整体存 JSONB；作业级排放条件另立标量列便于检索。
CREATE TABLE IF NOT EXISTS scan_jobs (
    id           TEXT PRIMARY KEY,
    q            DOUBLE PRECISION NOT NULL,
    physical_h   DOUBLE PRECISION NOT NULL,
    u            DOUBLE PRECISION NOT NULL,
    stability    CHAR(1)          NOT NULL,
    status       TEXT             NOT NULL,
    request      JSONB            NOT NULL,
    result       JSONB            NOT NULL,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT now()
);

-- 按创建时间倒序浏览最近作业。
CREATE INDEX IF NOT EXISTS idx_scan_jobs_created_at ON scan_jobs (created_at DESC);

-- 多源合成作业表：每行是一条可回查的多点源稳态合成作业。
-- 完整输入（含每个源的坐标/排放条件/抬升参数、每个受体坐标）与
-- 完整结果（每个受体的合成浓度 + 逐源贡献拆解）整体存 JSONB；
-- 作业级风况另立标量列便于检索。
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

-- 按创建时间倒序浏览最近多源作业。
CREATE INDEX IF NOT EXISTS idx_multisource_jobs_created_at ON multisource_jobs (created_at DESC);
