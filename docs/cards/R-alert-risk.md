# R. 알림과 장애 위험 지수

상태가 바뀐 순간을 알림으로 남기고(화면 알림 종, webhook), 과거 장애 기록과 수집 데이터의 관계를 주기적으로 학습해 **장애 위험 지수**로 보인다. 지수는 판단을 대신하지 않는 참고 지표이고, 시연에서 정확하지 않아도 **학습 → 점수 → 저장 → 화면 → 꺼내기**의 메커니즘이 실제로 돌아야 한다. **개념: 데이터 주권(같은 데이터로 학습), 업무 로그 알림.**

공통 설계 근거: 05 §0(바뀐 원칙), §5(알림), §6(장애 기록과 지수), §10(API), §11.2(설정), §12(타입·조립), §13(DDL), 02 §12.1(서버 상태·패널 임계치).

---

<a id="x1"></a>
## X1. 알림 Watcher · webhook · 장애 자동 기록 [하]

| 항목 | 내용 |
|---|---|
| 선행 | C4, B3 |
| 쓰는 경로 | `internal/manager/alert/`(stub 교체), `internal/manager/notify/webhook*.go` |
| 읽을 것 | **05 §5 전부**, §6.1(`auto`), §10(`/alerts`), §12(`alert`·`Alert` 타입), 02 §7(`Notifier`), C4 보고(`JudgeHost`, 패널 집계) |
| 넘겨주는 것 | 알림 목록·확인(D10, 실시간 이벤트 D4), webhook, 장애 자동 기록(R1의 학습 라벨) |

### 만들 것
- `notify.NewWebhook(url)`: `Alert`를 JSON으로 POST(타임아웃 3초). 2xx가 아니거나 실패하면 에러.
- `alert.New(d) *Watcher`, `Run(ctx)`: `Cfg.Every`(5초)마다 한 번 `step(now)`.
  - 시작할 때 `State.ListAlerts({State: firing})`로 열린 알림을 읽어 키별 상태를 이어받는다. 진행 중 장애(`ListIncidents`, end 0, source auto)도 이어받는다.
  - **host**: `Live.Hosts()`(+ `State.ListHosts()`) 각각에 `query.JudgeHost`. 상태가 ok가 아니게 바뀌면 firing(warn→warn, crit·silent→crit), ok로 돌아오면 resolve. warn↔crit 사이 변화는 기존 알림을 resolve하고 새로 연다.
  - **tile**: `query.EvalPanels`(C4)로 타일 상태를 얻는다. none·ok → warn·crit이면 firing, 돌아오면 resolve.
  - **log**: 지난 step 뒤의 로그를 `Metrics.QueryLogs({Levels: Cfg.LogLevels, From: last})`로 읽어 `(host, source)`별로 센다. firing 알림이 없거나 그 알림이 60초 넘었으면 새 알림 `"(E) n건 · <첫 메시지>"`, 60초 안이면 새로 만들지 않는다. 60초 동안 새 줄이 없으면 resolve.
  - **run**: `State.ListRuns({From: now − 10분})` 중 그 스텝의 최신 attempt이고 `sched.IsDead(run, step)`(step은 `d.Flows()`에서 찾음, 정의가 없으면 건너뜀)인 실행. 처음 본 것만 알림, 60초 뒤 resolve.
  - **risk**: `d.Risk()`가 있으면 지수가 `RiskWarn`·`RiskCrit`을 넘는 순간 firing, 내려오면 resolve.
  - 새 알림은 `State.AddAlert` 뒤, 수준이 `MinNotify` 이상이면 모든 Notifier에 보낸다(실패는 로그, Watcher는 계속).
  - **장애 자동 기록**(`AutoIncident`): crit 타일이 있거나 crit 서버가 2대 이상인 상태가 `IncidentAfter`(30초) 이어지면 `CreateIncident{Start: 그 상태가 시작된 시각, Scope: 해당 그룹(여럿이면 ""), Title: "자동: <첫 원인>", Source: "auto"}`. 모두 풀린 지 `IncidentClear`(60초)가 지나면 `UpdateIncident{End}`.
- `Mount(mux)`:
  - `GET /api/v1/alerts?state=firing|resolved|all&kind=&since=&limit=`(기본 firing, limit 200) → `{"alerts":[…]}`(05 §10 형식, firing 먼저 그다음 최신순).
  - `POST /api/v1/alerts/{id}/ack` → `AckAlert(id, now, "user:web(<주소>)")` → `204`. 없으면 404.

### 테스트 먼저 (필수, memstore, 가짜 시계, `step(now)`를 직접 부름, `httptest` webhook)

| 테스트 | 기대 |
|---|---|
| `TestHostTransition` | ok → crit 한 번 → firing 1 + POST 1 / 같은 상태 유지 → 추가 없음 / ok → resolved, POST 없음(info) |
| `TestTileTransition` | 처리 대기 2240(crit 2000) → firing tile, 키 `tile:matching/처리 대기` |
| `TestLogBurst` | 60초 안에 (E) 5줄 → 알림 1개, 메시지에 5건 / 61초 뒤 새 줄 → 새 알림 |
| `TestRunDead` | retry 0인 스텝 FAILED → run 알림 1 / retry 1인 스텝의 attempt 1 FAILED → 알림 없음 / KILLED → 알림 1 |
| `TestRiskThreshold` | Risk 25 → 65 → 20 → firing crit 1, resolve |
| `TestAutoIncident` | crit 타일 31초 → 장애 1(open), 풀린 뒤 61초 → end 채움 |
| `TestResumeFiring` | firing 알림이 있는 저장소로 새 Watcher → 같은 조건에서 중복 알림 없음 |
| `TestWebhookFail` | webhook 500 → Watcher 계속, 알림은 저장됨 |
| `TestAlertsAPI` | 목록 순서, ack 204, 없는 id 404 |

### 완료 기준
필수 테스트 통과. `demo.sh surge` 중 `match-01` 경고, 처리 대기 위험, `(E)` 로그 알림이 `/alerts`에 한 번씩(수동 확인은 보고에).

---

<a id="r1"></a>
## R1. 장애 위험 지수: 장애 기록 · 학습 · 점수 · API [상]

| 항목 | 내용 |
|---|---|
| 선행 | C2, B3 |
| 쓰는 경로 | `internal/manager/risk/`(`llm_model*.go` 제외, stub 교체) |
| 읽을 것 | **05 §6 전부**, §10(`/risk`, `/incidents`, `/export/risk-dataset`), §11.2(`risk`), §12(`risk` 공개 API), §13 |
| 넘겨주는 것 | 관제의 장애 위험 카드(D4), `#risk` 화면(D10), 알림의 risk 종류(X1), `eum risk train`, `eum incident import` |

### 목표
"기존 장애 기록 ↔ 수집 데이터"의 관계를 **우리 DB의 데이터로** 주기 학습해 지수로 낸다. 외부 라이브러리 없이 표준 라이브러리로 만들고, 사례가 모자라면 규칙 기반으로 계산해 그 사실을 화면에 밝힌다.

### 만들 것
1. **특징**(`features.go`): `FeatureDef{Name, Title, Metric, Metric2 string; HostAgg string /*max|sum|avg|count_ge*/; Threshold float64; TimeAgg string /*avg|max|sum*/; Source string /*metrics|runs_failed*/; RuleLo, RuleHi, RuleWeight float64}`. `DefaultFeatures()`는 05 §6.2 표 8개. `lag`는 `Metric − Metric2`(두 지표의 구간별 서버 합의 차).
2. **데이터셋**(`dataset.go`): `BuildDataset(ctx, m store.MetricStore, st store.StateStore, feats []FeatureDef, from, to int64, lead time.Duration) (Dataset, error)`.
   - 1분 창. 특징마다 `m.Query({Name: Metric, From, To, StepSec: 60})`(서버 전부)로 서버별 1분 평균을 받아 창마다 `HostAgg` → 같은 창 안 값은 이미 1분 평균이라 `TimeAgg`는 1분 창에서는 그 값 그대로(점수 때의 60초 창도 같은 함수로 계산되게 `step` 인자를 둔다).
   - `runs_failed`는 `st.ListRuns({From, To})`에서 창 안에 끝난 FAILED·TIMEOUT·KILLED·LOST 수.
   - 값이 없는 창의 특징은 0. 라벨은 05 §6.2(`[start − lead, end]`와 겹치면 1).
3. **모델**(`logreg.go`, `rule.go`): `init()`에서 `Register("logreg", …)`, `Register("rule", …)`.
   - `logreg.Fit`: 특징별 평균·표준편차(0이면 1)로 표준화 → 가중치 0에서 시작, 배치 경사 하강 1,000회(학습률 0.1), L2 λ 0.01, 양성 가중치 n/(2·n_pos), 음성 n/(2·n_neg). 시간순 앞 80%로 학습, 뒤 20%로 AUC(양성·음성이 모두 있을 때만, 아니면 0). 그 뒤 전체로 다시 학습. 결정적이어야 한다.
   - `logreg.Score`: `p = σ(w·z + b)`, Index = `round(100p)`, Factors = `w_i·z_i` 양수 상위 3개(Title·Value 포함).
   - `rule`: 학습 없음. 05 §6.2의 정규화·가중치. Factors = 가중 정규화 값 상위 3개.
   - `State`·`Restore`: 특징 이름, 평균, 표준편차, 가중치, 절편, 보고서를 JSON으로.
4. **엔진**(`engine.go`): `New(d)`는 `<DataDir>/risk/model.json`이 있으면 `Restore`.
   - `Run(ctx)`: `ScoreEvery`(10초)마다 `score(now)`, `TrainEvery`(10분)마다 그리고 시작 1분 뒤 한 번 `train(now)`.
   - `train`: `BuildDataset(now − TrainWindow, now)`. 장애 수 < `MinIncidents` 또는 양성 창 < 10이면 모드 `rule`, 노트 `"규칙 기반 · 학습 사례 부족(장애 n건, 최소 m건)"`. 아니면 설정 모델(`Cfg.Model`, 기본 logreg)로 `Fit` → `model.json`을 임시 파일 → rename으로 저장, 모드 `model`. 설정 모델이 `llm`이면 학습 없이 모드 `model`이고, `Score` 에러 때 `rule`로 대신 계산하고 노트를 단다.
   - `score`: 최근 60초를 같은 `BuildDataset` 규칙(step 60, 창 1개)으로 벡터 하나 → 모드의 모델로 `Score` → `Metrics.Write(Sample{Host "_eum", Name "risk.index", Value})` → 메모리에 현재 점수·수준·기여 보관. `Current()`가 그 값을 돌려준다.
   - 수준: `< Warn` ok, `< Crit` warn, 이상 crit.
5. **API**(`Mount`)
   - `GET /api/v1/risk` → 05 §10 형식. `history`는 `risk.index`의 최근 1시간(step 10).
   - `POST /api/v1/risk/train` → 지금 `train` 후 보고서(`FitReport`, 모드, 노트). 감사 `risk.train`.
   - `GET /api/v1/incidents?from=&to=`(기본 최근 30일), `POST /api/v1/incidents`(start·title 필수, source `manual`), `PATCH /api/v1/incidents/{id}`(end·title·note), `POST /api/v1/incidents/import`(CSV 본문 → `ImportIncidents`, `{"imported":n}`). 만들기·고치기·가져오기는 감사 `incident.*`.
   - `GET /api/v1/export/risk-dataset?from=&to=` → CSV(`ts,<특징 이름들>,label`, 기본 최근 7일).
6. **오프라인**: `TrainOffline(dataDir, cfg, w)`: 두 DB를 열고(`metricsql`·`statesql`) 같은 `train`을 돌려 보고서를 표로 찍고 `model.json`을 쓴다. `ImportIncidents(ctx, st, r, loc)`: 05 §6.1의 CSV(머리 줄 허용), 시각 두 형식, 잘못된 줄은 줄 번호를 담은 에러로 모아 돌려주되 맞는 줄은 넣는다.

### 테스트 먼저 (필수)

| 테스트 | 기대 |
|---|---|
| `TestLogregSeparable` | 특징 2개, 양성은 x1 큼 → AUC ≥ 0.95, x1 가중치 > 0, 같은 입력 두 번 같은 가중치 |
| `TestLogregImbalanced` | 양성 2%, 분리 가능 → 양성 평균 점수 > 음성 평균 점수 + 30 |
| `TestRuleModel` | queue 2000·p99 200(나머지 lo) → 지수 = 가중 합 공식 값, 기여 1위 queue 또는 p99 |
| `TestStateRoundTrip` | Fit → State → 새 모델 Restore → 같은 Score |
| `TestDatasetLabels` | 장애 10:00~10:10, lead 5분 → 09:55~10:10 창만 1 |
| `TestDatasetFeatures` | memstore에 서버 2대 cpu(50, 80), queue 합 → `cpu_max 80`, `hosts_hot 1`, `queue` 합, `lag` 차 |
| `TestEngineFallback` | 장애 1건 → 모드 rule, 노트에 "학습 사례 부족" |
| `TestEngineTrainAndScore` | 합성 7일(1분 간격, 장애 5건 전후로 queue·p99·log_errors 상승)로 학습 → 모드 model, AUC > 0.8, 장애와 같은 모양의 최근 60초 → 지수 ≥ 60, 평시 → < 30, `risk.index` 샘플이 저장됨 |
| `TestImportIncidents` | RFC3339·`YYYY-MM-DD HH:MM` 섞인 CSV, 깨진 줄 1개 → 맞는 줄 수, 에러에 줄 번호 |
| `TestRiskAPI` | `/risk` 형식(필드 전부), `/incidents` 만들기·고치기·목록, `/export/risk-dataset` 헤더와 행 수 |

### 완료 기준
필수 테스트 통과. `eum dev`(sqlite)에서 `/api/v1/risk`가 `mode: rule`과 노트를 돌려주고, E5가 병합되어 있으면 `eum seed` 뒤 `POST /risk/train`으로 `mode: model`이 됨(보고에 AUC).

### 하지 말 것
- 외부 ML 라이브러리를 쓰지 않는다. 학습을 별도 프로세스·파이썬으로 돌리지 않는다.
- 위험 지수로 실행을 막거나 바꾸지 않는다(참고 지표).
