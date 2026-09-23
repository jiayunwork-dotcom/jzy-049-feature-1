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

-- 多源叠加作业表：每行是一次「一批点源 + 一组受体点」的叠加稳态计算作业。
-- 作业输入与结果整体存 JSONB；公共气象条件另立标量列便于检索。
CREATE TABLE IF NOT EXISTS multi_source_jobs (
    id           TEXT PRIMARY KEY,
    wind_dir     DOUBLE PRECISION NOT NULL,  -- 气象罗盘风向角（度，正北起算顺时针，来向）
    u            DOUBLE PRECISION NOT NULL,
    stability    CHAR(1)          NOT NULL,
    status       TEXT             NOT NULL,  -- ok / partial（个别源被按零贡献处理）
    request      JSONB            NOT NULL,  -- 完整请求（源+受体全部输入）
    result       JSONB            NOT NULL,  -- 各受体合成浓度与逐项贡献/占比
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_multi_source_jobs_created_at ON multi_source_jobs (created_at DESC);

-- 多源作业的源明细表：一行对应提交的一个点源，逐源记录其原始输入，
-- 回查时能把「这次到底提交了哪几根烟囱、各自参数是什么」完整还原，
-- 而不是只回查一个总浓度数字。
CREATE TABLE IF NOT EXISTS multi_source_job_sources (
    job_id       TEXT NOT NULL REFERENCES multi_source_jobs(id) ON DELETE CASCADE,
    source_index INTEGER NOT NULL,
    source_id    TEXT NOT NULL,
    sx           DOUBLE PRECISION NOT NULL,  -- 源东向坐标 m
    sy           DOUBLE PRECISION NOT NULL,  -- 源北向坐标 m
    q            DOUBLE PRECISION NOT NULL,
    physical_h   DOUBLE PRECISION NOT NULL,
    source       JSONB NOT NULL,             -- 该源完整输入（含可选热抬升参数）
    PRIMARY KEY (job_id, source_index)
);

CREATE INDEX IF NOT EXISTS idx_multi_source_job_sources_job ON multi_source_job_sources (job_id);
