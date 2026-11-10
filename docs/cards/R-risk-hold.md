# R2·R3. 장애 위험도 — 확인 순서 · 지수 보정 · 요인 용인 (11-10)

> 운영자가 위험 지수가 높은 것을 봐도 **무엇을 먼저 확인할지**가 보이지 않고, 확인해서 이상이 없어도 빨간불이 계속 떠 있다. 세 가지를 한다.
> 1. **확인 순서**: 요인마다 어디서(서버·실행), 평소 대비, 확인 질문, 볼 곳 링크.
> 2. **지수 보정**: 로지스틱 회귀의 양성 가중치 때문에 부풀려진 지수를 실제 확률(05 §6.2 "100 × P(장애)")로 되돌린다.
> 3. **요인 용인**: 요인 하나에 `확인 중`·`용인`을 단다. 용인은 사유·한도·만료가 있고, 용인된 요인을 뺀 **미확인 지수**가 색과 알림을 정한다. 원래 지수는 그대로 저장·표시한다.
>
> 근거: ISA-18.2 셸빙(시간 제한·보류 목록), Checkmk·Icinga 확인(상태가 바뀌면 풀림), Alertmanager silence(작성자·사유·종료 필수), MS Defender 예외(용인은 점수 계산에서만 뺌), King & Zeng(2001) 사전 보정.
> 워커는 [README.md](README.md) **§3 규칙**과 [S-security.md](S-security.md)의 **"공통" 절**(쓰는 경로 밖 금지, 시연 :8080 건드리지 않기, gofmt·vet·test, 커밋 형식, **보고 15줄 이내**)을 따른다. 커밋 첫 줄은 `R2: …`·`R3: …`.

| 단계 | 카드 | 내용 | 맡는 곳 | 등급 |
|---|---|---|---|---|
| 0 | 계약 | 이 문서 | 오케스트레이터 직접 (**완료**) | — |
| 1 | **R2** | 지수 보정, 요인 위치·평소 값·확인 질문, 확인 표시(hold) 저장소·API·만료 감시, 미확인 지수, 알림 | 레인 A (**완료**) | 상 |
| 1 | **R3** | 관제 카드, `#risk` 화면, 감사 이름, mock | 레인 B(오케스트레이터) (**완료**) | 상 |
| 마무리 | R9 | 실제 바이너리 리허설, 문서(05 §5·§6·§10·§12·§13·§14) | 오케스트레이터 직접 (**완료**) | — |

**결과(11-10):** 계약과 다르게 정한 것 — 음수 값(처리량 부족)의 기본 한도는 `값 + |값|×0.2`(× 1.2면 값보다 작아져 걸자마자 풀림), `until` 0이면 기본, `ListRiskHolds`의 `to=0`은 상한 없음, llm 모드는 `false_warn`·`hit_warn`을 재지 않음(창마다 LLM을 부르게 되므로), 걸기·해제 직후 바로 다시 매겨 `open_index`가 다음 주기를 기다리지 않음, `stats.incidents`는 서로 다른 장애 수. 화면은 한도를 고치지 않으면 `limit`을 보내지 않고(서버의 마지막 값으로 기본 한도), 기여가 가장 큰 요인의 5% 미만이거나 0.05 미만인 요인은 "확인할 것"에서 뺀다(서버는 양수 기여를 다 줘서 평소와 같은 값이 섞여 옴). `eum risk train` 표에 절편 보정·평시 경고 비율·장애 전 경고 비율.
**보정 전후(같은 입력):** 장애 모양 99.9 → 92.4, 평시 6.80 → 0.06(TestEngineTrainAndScore, offset 4.83). 기존 테스트 기대값은 바꾸지 않음. 시연 seed: 창 10,081 · 양성 155 · 장애 6 · AUC 1.000 · `prior_offset` 4.159 · `false_warn` 0.07% · `hit_warn` 95.5%. 평시 중앙값 대기 큐 10.3 · p99 17.8 · CPU 25.3.
**리허설(별도 포트 18089, 실제 바이너리, seed 7일):** 평시 지수 0(보정 뒤). 접수율 6000 → 지수 0 → 28 → 69 → 92, 요인 p99·대기 큐·처리량 부족에 `match-01`. 조회 계정 POST 403, 사유 없음·한도 밖 400. 대기 큐 용인 → 원래 92 · 미확인 11(정상), `risk` 알림 resolved → 대기 큐가 한도(2,493)를 넘자 `system`이 `한도 초과 4161.1`로 풀고 감사 `risk.expire`, 알림 다시 crit(지수 100). p99 확인 중도 한도 초과로 풀림. 7일 통계 `대기 큐 1회 · 그동안 장애 1건`(폭주로 자동 장애가 열림). 화면에서 `용인…` 폼으로 건 용인이 서버에 들어감, 다른 요인만으로 100이면 "용인한 요인 1개를 빼도 남은 요인만으로 100". 매니저 재시작 뒤 살아 있는 용인 유지.

**화면 단순화(11-10, R4):** 사용자 지적("복잡하고 장황하다, 구성이 비직관적")으로 `#risk`를 위험도 한 줄 → 확인할 것 → 추이로 줄이고 이상 없음 기록·모델·장애 기록은 접힌 칸으로. 화면 말은 `용인` → **이상 없음**, `미확인 지수` → **위험도**(원래 지수는 **전체**), `확인 중` 버튼은 뺌(API는 그대로). 기여 막대·배수 열·안내 문장을 없앴다. 05 §6.4·§14.6.

R2·R3은 쓰는 경로가 겹치지 않으므로 동시에 진행한다. R3은 아래 계약만 믿고 mock으로 만든다.

## 계약

### 용어

| 말 | 뜻 |
|---|---|
| 원래 지수 `index` | 지금 특징 벡터 전부로 매긴 지수(지금과 같음). `risk.index`로 저장, 학습·차트용 |
| 미확인 지수 `open_index` | **용인(accept) 상태인 요인을 벡터에서 빼고** 같은 모델로 다시 매긴 값. `min(index, 그 값)`. 용인이 없으면 `index`와 같다. `risk.index_open`으로 저장 |
| 확인 표시 `hold` | 요인 하나에 사람이 단 표시. 종류 `ack`(확인 중: 내가 보고 있다, 지수 그대로) · `accept`(용인: 봤고 괜찮다, 미확인 지수에서 뺀다) |

- 벡터에서 뺀 특징은 모델이 "평소"로 본다: logreg는 평균(z = 0, 이미 그렇게 동작), rule은 0(정규화 0). 새 인터페이스·메서드 없이 `Vector`에서 이름을 빼서 부른다.
- **수준과 알림은 미확인 지수로 정한다.** `level`은 지금처럼 원래 지수의 수준, `open_level`이 새로 생긴다. 화면 색·`risk` 알림은 `open_level`.

### 확인 표시 규칙

| 항목 | ack(확인 중) | accept(용인) |
|---|---|---|
| 사유 | 없어도 됨 | **필수**(공백 제외 1~200자) |
| 만료 `until` | 기본 지금 + 30분 | 기본 지금 + 4시간 |
| 만료 최대 | 지금 + 24시간 | 지금 + 24시간 |
| 한도 `limit` | 기본 `값 × 1.2` | 기본 `값 × 1.2` |
| 지수 영향 | 없음(누가 보는지만 표시) | 미확인 지수에서 그 요인을 뺌 |

- `값`(value)은 서버가 거는 순간의 그 특징 값이다(요청에서 받지 않는다). 한도는 `값 ≤ limit ≤ max(2 × 값, 값 + 1)`. 범위를 벗어나면 400.
- 특징 하나에 살아 있는 표시는 **하나**다. 새로 걸면 이전 것을 `바꿈`으로 끝낸다(ack → accept로 올리기).
- **자동 해제**(점수 주기마다): `지금 ≥ until` → `만료`, `값 > limit` → `한도 초과 <값>`. 끝낸 사람은 `system`. 미확인 지수는 그 순간 다시 오르고 `risk` 알림이 조건대로 열린다.
- 모르는 특징 이름은 400. 지금 기여하지 않는 특징에도 걸 수 있다(누르는 사이 빠지는 경합을 막으려고).
- 용인 기록은 **학습 라벨에 쓰지 않는다**(장애 자동 기록이 지수를 쓰지 않는 것과 같은 이유).

### 모델 `internal/model`

```go
// RiskHold는 위험 요인 하나에 사람이 단 확인 표시다(05 §6.4).
type RiskHold struct {
	ID      int64   `json:"id"`
	Feature string  `json:"feature"`  // 특징 이름(risk.DefaultFeatures)
	Kind    string  `json:"kind"`     // ack | accept
	Reason  string  `json:"reason"`
	Value   float64 `json:"value"`    // 걸 때의 값
	Limit   float64 `json:"limit"`    // 이 값을 넘으면 풀림
	At      int64   `json:"at"`       // 건 시각(ms)
	By      string  `json:"by"`       // actor
	Until   int64   `json:"until"`    // 만료 시각(ms)
	EndedAt int64   `json:"ended_at"` // 0 = 살아 있음
	EndedBy string  `json:"ended_by"`
	EndNote string  `json:"end_note"` // 해제 | 만료 | 한도 초과 <값> | 바꿈
}
```

### 저장소 `StateStore` (메서드 추가, sqlite·memstore 같은 동작, storetest 검사)

```go
AddRiskHold(ctx context.Context, h model.RiskHold) (int64, error)
EndRiskHold(ctx context.Context, id, ts int64, by, note string) error   // 없거나 이미 끝났으면 ErrNotFound
ListRiskHolds(ctx context.Context, from, to int64) ([]model.RiskHold, error) // [at, ended_at(0이면 ∞)]가 [from,to)와 겹침, at 내림차순
```

```sql
CREATE TABLE IF NOT EXISTS risk_holds (
  id INTEGER PRIMARY KEY AUTOINCREMENT, feature TEXT NOT NULL, kind TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  value REAL NOT NULL, lim REAL NOT NULL, at INTEGER NOT NULL, by TEXT NOT NULL DEFAULT '', until INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0, ended_by TEXT NOT NULL DEFAULT '', end_note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_risk_holds_at ON risk_holds(at);
```

### API

| 요청 | 응답 | 역할 |
|---|---|---|
| `GET /api/v1/risk` | 아래 형식(지금 필드 + 새 필드) | 조회 |
| `GET /api/v1/risk/holds?from=&to=` | `200 {"holds":[RiskHold…], "stats":[{feature,title,count,ms,incidents}], "from", "to"}` 기본 최근 7일 | 조회 |
| `POST /api/v1/risk/holds` `{"feature","kind","reason","until"?,"limit"?}` | `201 RiskHold`. 잘못된 값 `400 {"error"}` | 운영 |
| `DELETE /api/v1/risk/holds/{id}` | `204`(끝냄, `해제`). 없거나 이미 끝남 `404` | 운영 |

`GET /api/v1/risk` (굵게 = 새 필드)

```jsonc
{
  "now": 0, "index": 72, "level": "crit", "mode": "model",
  "open_index": 24, "open_level": "ok",                       // 새
  "model": { "kind": "logreg", "trained_at": 0, "windows": 10080, "positives": 61, "incidents": 6, "auc": 0.93, "lead_sec": 300,
             "prior_offset": 5.1,                               // 새: 절편에서 뺀 ln(평시 창 수/장애 창 수). rule이면 0
             "false_warn": 0.012, "hit_warn": 0.71 },           // 새: 학습 구간 평시 창 중 경고선 이상 비율, 장애 창 중 경고선 이상 비율(rule도 계산)
  "factors": [{                                                 // 기여 양수 전부, 기여순(최대 8)
    "feature": "queue", "title": "대기 큐", "value": 2240, "contrib": 1.9,
    "baseline": 380,                                            // 새: 학습 구간 평시 창(라벨 0)의 중앙값. 학습 전이면 null
    "check": "유입이 늘었나, 처리가 줄었나(처리량 부족과 같이 본다)", // 새: FeatureDef.Check
    "where": [{"kind": "host", "id": "match-01", "label": "", "value": 2240}], // 새: 아래 표
    "hold": null                                                // 새: 살아 있는 RiskHold 또는 null
  }],
  "holds": [RiskHold…],                                         // 새: 살아 있는 표시 전부(factors에 없는 특징 포함)
  "incidents": [Incident…],                                     // 새: 진행 중(end 0)인 장애, start 내림차순
  "note": "", "history": [[ts, v]…],
  "history_open": [[ts, v]…]                                    // 새: risk.index_open 최근 1시간(step 10)
}
```

**`where` — 요인이 어디서 나왔나**(최근 60초, 값 큰 순)

| 특징 | kind | id · label · value | 개수 |
|---|---|---|---|
| `cpu_max`, `mem_max`, `p99` | `host` | 서버 id · "" · 그 서버의 값 | 상위 3 |
| `hosts_hot` | `host` | CPU 70% 이상 서버 · "" · CPU | 상위 5 |
| `queue` | `host` | `biz.order.queue`가 0보다 큰 서버 · "" · 값 | 상위 3 |
| `lag` | `host` | `in_tps − tps`가 0보다 큰 서버 · "" · 차 | 상위 3 |
| `log_errors` | `log` | 서버 id · 로그 출처(source) · 줄 수 | 상위 3 |
| `runs_failed` | `run` | exec_id · `<flow>/<step>` · 0 | 최근 5 |

**`check` — 기본 확인 질문**(`FeatureDef.Check`, 특징 표 한 줄에 같이 둔다)

| 특징 | check |
|---|---|
| `cpu_max` | 어느 프로세스가 CPU를 쓰는가 · 배치 시간대와 겹치는가 |
| `mem_max` | 메모리가 계속 늘고 있는가(누수) · 같은 서버의 다른 요인도 올랐나 |
| `hosts_hot` | 여러 서버가 함께 올랐다면 유입 증가 같은 공통 원인부터 |
| `queue` | 유입이 늘었나, 처리가 줄었나(처리량 부족과 같이 본다) |
| `p99` | 느린 서버가 한 대인가 전부인가 · CPU·대기 큐와 같이 올랐나 |
| `lag` | 처리가 유입을 못 따라간다 · 처리 서버 상태와 대기 큐 추이 |
| `log_errors` | 새로운 오류 문구인가, 늘 나던 것인가 |
| `runs_failed` | 재시도가 남았는가 · 후행 배치가 막혔는가 |

**`stats` — 반복 용인**(구간 안의 `accept`만, 특징별): `count` 건수, `ms` 구간 안에 걸려 있던 시간 합, `incidents` 그 용인이 걸려 있는 동안 겹친 장애 수. 화면은 `count ≥ 3 && incidents == 0`이면 "기준 조정·재학습 검토"를 띄운다.

### 감사 기록

| action | 화면 이름 | target | detail |
|---|---|---|---|
| `risk.ack` | 위험 요인 확인 중 | `risk:<feature>` | `<제목> <값> · 한도 <limit> · ~<HH:MM>[ · 사유: …]` |
| `risk.accept` | 위험 요인 용인 | `risk:<feature>` | 같은 형식(사유 있음) |
| `risk.release` | 용인 해제 | `risk:<feature>` | `<제목> #<id>` |
| `risk.expire` | 용인 자동 해제 | `risk:<feature>` | `<제목> #<id> 만료` 또는 `한도 초과 <값>` (actor `system`) |

### 알림 (05 §5)

`risk` 알림은 **미확인 지수**로 판정한다. 메시지: 용인이 없으면 지금과 같고(`장애 위험 지수 72 (위험)`), 있으면 `장애 위험 지수 미확인 24 (경고) · 전체 72, 용인 1`. `Value`는 미확인 지수.

### 지수 보정 (05 §6.2)

- `logreg.Fit`: 양성 가중치 n/(2·n_pos), 음성 n/(2·n_neg)로 학습한 로짓은 ln(n_neg/n_pos)만큼 위로 밀려 있다. 최종 모델(전체 데이터)에서 `prior_offset = ln(n_neg/n_pos)`(어느 한쪽이 0이면 0)를 구해 점수 때 `σ(w·z + b − prior_offset)`로 매긴다. AUC는 순위만 보므로 변하지 않는다. 상태 JSON에 `prior_offset`을 둔다.
- 옛 `model.json`(필드 없음)을 읽으면 보고서의 창·양성 수로 계산해 채운다.
- `false_warn`·`hit_warn`은 `train`에서 학습 데이터 전체를 그 모드의 모델로 매겨 계산한다(학습 구간 기준, rule 모드도). 경고선은 `Cfg.Warn`.
- 경고·위험선(30·60)은 그대로 둔다. 이제 "5분 안 장애(또는 장애 중) 확률 30%·60%"라는 뜻이 된다.

---

## R2. 백엔드 (레인 A, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/risk/**`, `internal/manager/store/**`(store.go·statesql·memstore·storetest), `internal/model/model.go`(더하기만), `internal/manager/alert/**`(위험 판정·메시지·Deps), `internal/manager/webauth/role.go`·`role_test.go`(두 줄), `internal/manager/manager.go`(Risk 조립 부분만), 각 패키지 테스트 |
| 고치지 않는 것 | `internal/manager/web/**`, `docs/**`, 그 밖의 패키지 |
| 읽을 것 | 위 계약, `risk/engine.go`·`logreg.go`·`rule.go`·`dataset.go`·`api.go`, `store/store.go`와 statesql의 incidents·audit 부분, `alert/alert.go`의 `stepRisk`·`reconcile`, `audit/audit.go`(`Record`·`Write`), `manager.go`의 risk·alert 조립 |

1. **보정**: 위 "지수 보정". `logreg` 상태에 `prior_offset`, 옛 파일 호환. `FitReport`에 `PriorOffset`·`FalseWarn`·`HitWarn`(JSON `prior_offset`·`false_warn`·`hit_warn`), `Baseline map[string]float64`(JSON `baseline`, 평시 창 중앙값, rule 모드에서도 `train`이 채움). `model.json`에 함께 저장·복원.
2. **요인 정보**: `FeatureDef.Check`(위 표), `Factor`에 `Baseline *float64`·`Check`·`Where []Place`·`Hold *model.RiskHold`(JSON 위 형식). `Place{Kind, ID, Label string; Value float64}`. 모델의 `Score.Factors`는 기여 양수 전부(기여순, 최대 8). `where`는 점수 주기마다 최근 60초로 따로 구한다(서버별 시계열 `Metrics.Query`, 로그 `QueryLogs`, 실행 `ListRuns` — 데이터셋 함수와 같은 판정).
3. **확인 표시**: 저장소 3메서드 + DDL. 엔진이 시작할 때 살아 있는 표시를 읽어 메모리에 두고, 점수 주기마다 자동 해제(감사 `risk.expire`, actor `system`) → 미확인 지수 계산 → `risk.index`·`risk.index_open` 저장. `POST`·`DELETE`·`GET /risk/holds`는 위 규칙대로(감사 `risk.ack`·`risk.accept`·`risk.release`는 성공했을 때만). 시각 표시는 엔진의 `Loc`.
4. **API**: `GET /risk`에 새 필드. `Current()`는 지금처럼 원래 점수를 돌려주고, 미확인 쪽은 새 메서드(예: `Open() (index float64, level string, holds int, ok bool)`)로.
5. **알림**: `alert.Deps.Risk`를 미확인·원래 지수·용인 수를 돌려주게 바꾸고(구조체 필드라 바꿔도 된다) 위 메시지. `manager.go` 조립을 맞춘다.
6. **역할 표**: `"POST /api/v1/risk/holds": RoleOperator`, `"DELETE /api/v1/risk/holds/{id}": RoleOperator`, 테스트에 두 줄.

**먼저 쓸 테스트**

| 테스트 | 기대 |
|---|---|
| `TestLogregPriorOffset` | 양성 2% 데이터 → `prior_offset ≈ ln(49)`, 보정 뒤 평시 창 평균 확률이 실제 양성 비율의 3배 안 · 보정 전보다 낮음, AUC 그대로 |
| `TestLogregRestoreOld` | `prior_offset` 없는 옛 상태 JSON → 보고서 수로 채워 같은 점수 |
| `TestBaselineAndRates` | 합성 데이터 학습 → `baseline`이 평시 중앙값, `false_warn`·`hit_warn`이 0~1이고 hit > false |
| `TestFactorWhere` | memstore에 서버 3대 CPU(50, 92, 80) → `cpu_max`의 where 첫째가 92인 서버, `hosts_hot` where 2대, `runs_failed` where에 exec_id |
| `TestHoldRules` | accept 사유 없음 400, limit 범위 밖 400, until 24시간 넘음 400, 모르는 특징 400, 같은 특징에 다시 걸면 이전 것 `바꿈`, DELETE 204 → 다시 404 |
| `TestHoldOpenIndex` | 대기 큐·p99가 높은 상태에서 queue accept → `open_index < index`, `open_level` 낮아짐, `factors`의 queue에 hold, `risk.index_open` 저장 |
| `TestHoldAutoRelease` | 가짜 시계 until 지남 → `만료`로 끝, 값이 limit 넘음 → `한도 초과`로 끝, 둘 다 감사 `risk.expire`(actor system) |
| `TestHoldStats` | accept 3건(그중 1건 동안 장애) → 그 특징 `count 3, incidents 1`, ack는 세지 않음 |
| `TestRiskAlertOpen` | 원래 72·미확인 24 → `risk` 알림 없음(경고선 30), 용인 해제 → crit 알림, 메시지 형식 |
| storetest | Add·End·List(겹침·내림차순·이미 끝난 것 End → ErrNotFound) sqlite·memstore 같음 |

기존 테스트(`TestLogregImbalanced`, `TestEngineTrainAndScore`의 "장애 모양 ≥ 60, 평시 < 30" 등)가 보정 때문에 깨지면 기대를 바로 고치지 말고, 숫자를 확인해 **보정된 확률의 뜻에 맞는지** 판단한 뒤 고치고 그 근거를 보고에 적는다.

**보고**에 덧붙일 것: 보정 전후 지수 예(같은 입력), 시연 seed 데이터의 `prior_offset`·`false_warn`·`hit_warn`, 바꾼 기존 테스트와 이유.

---

## R3. 화면 (레인 B, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/web/static/**`만 |
| 읽을 것 | 위 계약, `pages/dashboard.js`(`riskCard`·`gaugeSVG`), `pages/risk.js`, `auditfmt.js`, `ui.js`, `css/`, `mock/alert.js` |

- **관제 카드**: 큰 숫자 = 미확인 지수, 게이지는 원래 지수까지 그리되 미확인 몫은 수준 색으로 채우고 용인 몫은 빗금. 배지는 `open_level`. 그 아래 "확인할 것" 상위 2개(용인 안 된 요인): `순위 요인 값 (평소 n) · 어디`. 용인이 있으면 `용인 n` 칩. 진행 중 장애가 있으면 맨 위 한 줄 `장애 진행 중 HH:MM~ 제목`.
- **`#risk` 화면**: 위에서 아래로 ① 지수(미확인 큰 숫자, 원래 지수, 수준, 진행 중 장애) ② **확인할 것** 표(순위 · 요인 · 지금/평소 · 기여 막대 · 어디 링크 · 확인 질문 · 상태와 단추 `확인 중`·`용인…`·`해제`, 용인 폼 = 사유·기간(1h/4h/8h/24h)·한도) ③ 지수 추이(원래·미확인 두 선) ④ 용인 기록(7일 통계, 반복 용인 경고, 목록) ⑤ 모델(보정·평시 경고 비율 포함) ⑥ 장애 기록.
- **어디 링크**: `host` → `#host/<id>/overview`, `log` → `#host/<id>/overview`, `run` → `#run/<exec_id>`, `runs_failed` 자체 → `#runs?status=failed&range=1h`.
- **단추 권한**: `gate(…, 'operator')`.
- **감사 이름**: `auditfmt.js`에 위 네 action.
- **mock**: `/risk` 새 필드, `/risk/holds` GET·POST·DELETE를 계약대로(메모리 안 목록, 규칙 검사 포함, 미확인 지수는 용인된 요인의 기여 비율만큼 낮춤).
