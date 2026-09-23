# 稳态高斯烟羽扩散服务（Gaussian Plume Service）

把环评/应急里反复手算的**稳态高斯烟羽（连续点源）地面浓度**做成一个后端服务：
喂入排放条件（源强、源高、风速、稳定度、下风向距离网格），逐受体点算地面浓度，
并把每次扫描作为**可回查作业**持久化到 PostgreSQL 16。

在单源能力之上还叠了一层**多点源稳态合成**：一次喂入一批烟囱（各自水平位置、
排放条件、物理源高，热抬升可选）+ 统一风向风速，直接算出任意受体点（厂区边界、
最近居民点）上所有源**共同贡献的合成地面浓度**，并逐源拆解贡献与占比。

只做这一件事。天气预报展示、城市空气质量看板、标准大气温度层结查表、复杂地形
流场、非稳态扩散、多受体等值线图**不在范围内**。

- 运行时锁定：**Go 1.22**、**Gin**、**PostgreSQL 16**、Docker Compose 编排。

---

## 1. 物理模型

### 1.1 高斯烟羽稳态解（含地面全反射镜像项）

```
C(x,y,z) = Q / (2π·σy·σz·u)
           · exp(−y²/(2σy²))
           · [ exp(−(z−H)²/(2σz²)) + exp(−(z+H)²/(2σz²)) ]
                         └ 直接项 ┘     └ 地面全反射镜像项 ┘
```

**钉死的实现选择（不再随调用变化）：**

1. 前置系数用 **`2π`**；
2. 方括号**保留镜像项**。只算直接项会把地面源轴线浓度少算一半；
3. 单位：`Q` kg/s，长度（`H,x,y,z,σy,σz`）m，`u` m/s，浓度 `C` kg/m³
   （`1 kg/m³ = 1e6 mg/m³ = 1e9 µg/m³`）。

**地面源解析极限（自动化测试锁死）：** 当 `H=0`、受体在源高处 `z=0`、轴线 `y=0`：
两项相等并为一块，括号 `= 2·exp(0) = 2`，故

```
C(x,0,0)|_{H=0} = Q / (π·σy·σz·u)
```

### 1.2 扩散参数 σy、σz（Pasquill–Gifford，分段幂律）

对 P-G 曲线图做 **Turner 风格分段幂律**拟合，σy、σz **都**随距离按幂律增大
（指数 0.7–0.95，**不是与距离一次正比**），并随稳定度 A（极不稳定）→F（稳定）变小：

```
近段 x≤1000 m：σ = c·x^d
远段 x>1000 m：σ = σ(1000)·(x/1000)^p        # 在 x=1000 严格连续
```

钉死的 P-G 锚定值与反解近段系数（x 以 m 计）：

| 稳定度 | σy(100) | σy(1000) | cy | dy | σz(100) | σz(1000) | cz | dz |
|---|---|---|---|---|---|---|---|---|
| A | 26.0 | 170.0 | 0.6082 | 0.8155 | 16.0 | 130.0 | 0.2424 | 0.9098 |
| B | 19.0 | 130.0 | 0.4059 | 0.8352 | 12.0 | 90.0 | 0.2133 | 0.8751 |
| C | 12.5 | 90.0 | 0.2411 | 0.8573 | 8.5 | 60.0 | 0.1706 | 0.8487 |
| D | 8.0 | 70.0 | 0.1045 | 0.9420 | 4.5 | 26.0 | 0.1348 | 0.7618 |
| E | 6.0 | 45.0 | 0.1067 | 0.8751 | 2.8 | 14.0 | 0.1120 | 0.6990 |
| F | 4.0 | 28.0 | 0.0816 | 0.8451 | 1.5 | 7.5 | 0.0600 | 0.6990 |

远段统一指数：**σy p=0.90，σz p=0.70**。σz 的 c、d 同向不增，任意 x>0 严格 A>…>F；
σy 在物理区间 x≳10 m 严格有序（源自身尺度内的 D/E 微调无物理意义）。可靠拟合域约
100 m–10 km，服务允许 x>0，更远为按钉死公式的外推。

### 1.3 有效源高与 Briggs 热抬升（可选）

调用方**要求叠抬升**时才计算：`H = hs + ΔH`，物理源高 `hs` 绝不直接冒充已抬升高。
不要求抬升时 `H = hs`；也可直接给定 `effective_h`（与抬升互斥）。

Briggs（1971/1975）热（浮力）抬升，钉死：

```
浮力通量   F = g·Vs·Ds²·(Ts−Ta)/(4·Ts)                    g=9.80665
中性/不稳定最终抬升:
   F < 55 :  ΔH = 21.425·F^(3/4)/u
   F ≥ 55 :  ΔH = 38.71 ·F^(3/5)/u
稳定(E/F)且给出 -dTa/dz>0:
   N² = (g/Ta)·(-dTa/dz − Γd),  Γd=0.0098 K/m
   ΔH = 2.6·(F/(u·N²))^(1/3)     # 未给层结时稳定类回退中性式
有限距离截断: ΔH(x) = min(ΔH_final, 1.6·F^(1/3)·x^(2/3)/u)
```

纯动量抬升不在范围内。

### 1.4 多点源稳态合成（坐标投影 + 线性叠加）

多源场景下各源到同一受体的方向与风向一般不重合，先把源与受体摆进**同一个
平面坐标系**，再按统一风向把每个"源→受体"位移投影成下风分量与侧风分量：

**钉死的风向角约定（全服务唯一，所有源与受体统一套用，没有第二套）：**

- 平面坐标系：x 轴朝东、y 轴朝北，原点任意但一次作业内一致；
- `wind_angle_deg`（度）是风**吹向**（去向）的方位角，自 +x 轴起算、**逆时针为正**
  （纯数学约定：0°=吹向正东，90°=吹向正北）；风的来向方位角 = θ+180°；
- 投影（d = 受体 − 源）：

```
x' =  dx·cosθ + dy·sinθ      （沿风下风分量）
y' = −dx·sinθ + dy·cosθ      （侧风分量，+y' 在风向左侧）
```

每个源单独贡献仍走 §1.1 的原公式（`X=x', Y=y', Z=0`，σ 按 x' 取，热抬升按
该源到该受体的 x' 做 1/3 律截断），数学一字不改。`x'≤0`（受体不在该源下风
半平面，即正侧风或上风）时，稳态烟羽解在该半平面的自然延拓为零 —— 该源
贡献取 0，不需要额外写"丢弃源"的分支。

**合成规则（钉死）**：同一受体点的地面浓度 = 全部源各自贡献之和（高斯烟羽对
源强线性，稳态线性叠加）。每个源附带 `share = c_i / Σc` 占比，方便定位主导源。

### 1.5 被自动化测试锁死的物理判据

见 `internal/plume/plume_test.go`、`internal/dispersion/dispersion_test.go`：

- **源强线性**：Q 翻倍、其余不变 → 各点浓度精确翻倍；
- **风速影响**：约定 σ 不显式依赖 u，u 翻倍 → 轴线浓度下降（精确减半）；
- **稳定度影响**：A→F 同距离 σz 变小，地面轴线沿程分布形态改变；
- **地面源镜像合并**：H=0 轴线地面浓度等于解析极限 `Q/(πσyσzu)`，且恰为只算直接项的 2 倍；
- 另含横向偏移降浓度、分段连续、P-G 锚定值、幂律非线性等校验。

多源合成的判据见 `internal/multisource/service_test.go`、`internal/projection/projection_test.go`：

- **单源退化**：只放一个源且落在受体下风轴线上时，多源结果与单源公式**逐位一致**；
- **线性叠加**：两源各自单跑的浓度之和，与一起提交的合成浓度**逐位相等**
  （位置/源高/稳定度任意组合）；
- **上风/侧风压低**：源在受体正上风、正侧风时贡献为零，偏轴 10° 时远小于正下风；
- **坏源隔离**：单个源排放条件非法只影响它自己（零贡献 + 原因），其余源照常叠加。

---

## 2. 模块划分（各自独立，非单文件堆叠）

```
cmd/server              程序入口：连库（带退避重试）、预置示范作业、起 HTTP
internal/model          核心领域类型与单位约定
internal/dispersion     Pasquill–Gifford σy/σz 分段幂律
internal/plume          高斯烟羽稳态解（镜像项、解析极限）
internal/rise           Briggs 热抬升（浮力通量、分段、有限距离截断）
internal/projection     共享平面坐标系投影（唯一风向角约定，下风/侧风分解）
internal/multisource    多源合成：source.go 单源贡献 / aggregate.go 累加占比 / service.go 编排入库
internal/validate       结构化输入校验（作业级 / 点位级）
internal/job            单源扫描编排、逐点计算、有效源高解析、作业入库与回查、示范作业
internal/store          PostgreSQL 16 持久化（pgx/v5，启动幂等迁移）
internal/api            Gin HTTP 接口
internal/config         环境变量配置
migrations/             SQL 迁移（store 内嵌同一份，启动自动应用）
```

---

## 3. HTTP API

基址 `/api/v1`，JSON。错误统一形如：
```json
{"error":{"message":"输入校验失败","fields":[{"field":"u","reason":"风速 u=0 必须为正"}]}}
```

### 3.1 单受体点计算 — `POST /api/v1/calculate`

返回该点浓度及所用 σy、σz、有效源高。

```bash
curl -s localhost:8080/api/v1/calculate -H 'Content-Type: application/json' -d '{
  "q": 0.1, "physical_h": 0, "u": 5, "stability": "D",
  "x": 1000, "y": 0, "z": 0
}'
# {"c":3.498...e-6,"effective_h":0,"sigma_y":70,"sigma_z":26}
```

叠 Briggs 抬升（可选）：
```json
"physical_h": 80,
"buoyancy_rise": {"vs": 20, "ds": 3, "ts": 400, "ta": 283}
```
响应额外给 `delta_h`、`rise_regime`。或直接给 `"use_given_h": true, "effective_h": 60`
（与 `buoyancy_rise` 互斥）。

### 3.2 提交下风向扫描作业 — `POST /api/v1/scans`

逐点算地面浓度并**落库**，返回作业标识 `job_id`。
个别网格点非法只影响该点（该点带 `error`，作业 `status:"partial"`），其余照算。

```bash
curl -s localhost:8080/api/v1/scans -H 'Content-Type: application/json' -d '{
  "q": 0.1, "physical_h": 0, "u": 5, "stability": "D",
  "distances": [100, 200, 500, 1000, 2000, 5000]
}'
# 201 {"job_id":"...","status":"ok","effective_h":0,"points":[...],"created_at":"..."}
```

入参字段：`q, physical_h, u, stability, distances[]`，可选 `y`、`z`（整网同一横/高偏移，
缺省 0）、`buoyancy_rise{...}` 或 `use_given_h+effective_h`。

### 3.3 按标识回查 — `GET /api/v1/scans/:id`

返回该作业完整输入与结果；不存在返回 `404`。

```bash
curl -s localhost:8080/api/v1/scans/00000000-0000-0000-0000-000000000001
```

### 3.4 提交多点源合成作业 — `POST /api/v1/multisource-jobs`

一批点源 + 统一风向风速 + 一批受体点，逐受体算**合成地面浓度**并逐源拆解
贡献与占比，落库后返回作业标识 `job_id`。

```bash
curl -s localhost:8080/api/v1/multisource-jobs -H 'Content-Type: application/json' -d '{
  "wind_angle_deg": 0, "u": 5, "stability": "D",
  "sources": [
    {"name": "1号炉", "x": 0,   "y": 0,   "q": 0.10, "physical_h": 60},
    {"name": "2号炉", "x": 0,   "y": 150, "q": 0.05, "physical_h": 40,
     "buoyancy_rise": {"vs": 18, "ds": 2.5, "ts": 390, "ta": 283}}
  ],
  "receptors": [
    {"name": "厂界东",     "x": 1000, "y": 0},
    {"name": "最近居民点", "x": 1200, "y": 300}
  ]
}'
# 201 {"job_id":"...","status":"ok","receptors":[
#   {"index":0,"receptor_name":"厂界东","total_c":...,
#    "contributions":[{"source_index":0,"source_name":"1号炉",
#      "downwind_x":1000,"crosswind_y":0,"effective_h":60,"c":...,"share":0.93,"status":"ok"},
#     {"source_index":1,"source_name":"2号炉",...,"share":0.07,"status":"ok"}]},
#   ...]}
```

- 风向角约定见 §1.4（**全服务唯一**：风吹向方位角，自 +x 起算逆时针为正）；
- 每个源可独立挂 `buoyancy_rise`（热抬升按该源到各受体的下风距离截断）；
- 单个源非法（如 `q<0`、`physical_h<0`）不拖垮整批：该源零贡献、
  `status:"invalid"` 并附 `reason`，作业 `status:"partial"`，其余源照常叠加；
- 作业级字段非法（风速非正、稳定度非法、源批/受体批为空、风向角非有限）→ 400。

### 3.5 回查多源作业 — `GET /api/v1/multisource-jobs/:id`

返回该作业**完整输入**（每个源的坐标/排放条件/抬升参数、每个受体坐标）与
**完整结果**（每个受体的合成浓度 + 逐源贡献/占比拆解）；不存在返回 `404`。

另有 `GET /healthz`。

---

## 4. 预置地面源示范作业（可手工核对）

服务启动幂等预置固定 ID `00000000-0000-0000-0000-000000000001`：
`Q=0.1 kg/s, u=5 m/s, H=0（地面源）, D 类`，网格 `[100,200,500,1000,2000,5000] m`。

在 `x=1000 m`：σy=70、σz=26（P-G 锚定值），

```
C = 0.1 / (π·70·26·5) ≈ 3.498e-6 kg/m³ ≈ 3498 µg/m³
```

正是地面源解析极限 `Q/(πσyσzu)`，可逐项手工对上。重启不重复插入、不覆盖。

---

## 5. 运行

```bash
docker compose up --build      # 构建并起 db(postgres:16) + api(:8080)
```

- db 用 `pg_isready` 健康检查，api 等 db 真就绪再启动（api 端另有连接退避重试）；
- 数据存命名卷 `plume_pgdata`，**进程/容器重启后历史作业仍可回查**；
- 并发提交：每作业独立 UUID 行 INSERT（`ON CONFLICT DO NOTHING`），互不串写、不覆盖。

环境变量：`HTTP_ADDR`（默认 `:8080`）、`DATABASE_URL`、`SEED_DEMO`（默认 `true`）。

本地直接运行（需自备 Postgres 16）：
```bash
go run ./cmd/server
# DATABASE_URL=postgres://plume:plume@localhost:5432/plume?sslmode=disable
```

---

## 6. 测试

```bash
go test ./...                 # 纯单元/HTTP 测试，不依赖数据库
go test -race ./...           # 含并发竞争检测
DATABASE_URL=postgres://plume:plume@localhost:5432/plume?sslmode=disable \
  go test ./internal/store/   # 额外跑 PostgreSQL 持久化/并发/示范作业集成测试
```

集成测试在未设置 `DATABASE_URL` 时自动 `skip`，因此 `go test ./...` 无需外部数据库。
覆盖：源强线性、风速影响、稳定度影响、地面源镜像解析极限、非法输入（结构化错误）、
坏点隔离、并发不串写、示范作业幂等与手算值；多源侧覆盖：坐标投影约定、单源退化
与老公式逐位一致、双源线性叠加、上风/侧风压低、坏源隔离、占比拆解、并发不串写。
