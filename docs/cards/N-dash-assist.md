# 카드 N — 보드 AI 초안 (2026-11-12)

> 요청(사용자, 11-12): 사용자 보드를 YAML·편집기로 손수 만드는 대신 LLM과 같이 쓰는 입력을 붙인다.
> 실제 연동만 붙이면 되게 만들고, 시연은 Claude에 붙인다(인터넷이 안 되는 곳일 수 있다).

## 1. 결정

| # | 결정 | 이유 |
|---|---|---|
| 1 | 새 경로 `POST /api/v1/assist/dash` 하나(`model.DashAssistReq/Resp`). 플로우 초안(`/assist/flow`)과 나란히, 구현은 `dash` 패키지(`dash/assist.go`) | 지표 목록·패널·검증이 모두 `dash`에 있다. LLM은 이미 있는 `llm.LLM`(05 §12)을 `dash.Deps.LLM`으로 받는다. 새 인터페이스 없음 |
| 2 | 요청 하나로 세 가지: `text`(+`dash`) = LLM 초안, `prompt_only` = 보낼 프롬프트만, `yaml` = LLM 없이 검사만(붙여넣기) | LLM이 연결되지 않아도(사내망·인터넷 없음) 프롬프트를 밖의 LLM에 넣고 답을 붙여 넣어 **오늘 바로** 쓴다. 종료 보고서의 "프롬프트 그대로"와 같은 생각 |
| 3 | **초안은 저장하지 않는다.** 편집기 정의를 갈아 끼우고 미리보기에 실제 값으로 그린다. 저장은 지금의 PUT(운영·감사·409) | 플로우 초안과 같다(05 §9). 보드는 미리보기가 실제 값이라 틀린 지표가 바로 보인다 |
| 4 | 검사 두 단: 형식(`model.ValidateDash`) + **참조**(없는 지표·패널·타일·서비스·그룹·서버·배치판) | LLM이 가장 자주 틀리는 것은 그럴듯한 지표 이름을 지어내는 것이다 |
| 5 | 검사에 걸리면 오류를 붙여 **한 번만** 다시 묻는다(`Tries` 최대 2). 그래도 틀리면 초안과 오류를 같이 돌려준다 | 대개 한 번에 고쳐진다. 더 물으면 시연에서 기다림만 길어진다 |
| 6 | 고칠 때는 지금 정의 전체를 주고 **정의 전체**를 다시 받는다(바뀐 위젯만 받기는 뒤로) | 검사·되돌리기가 단순하다. 위젯 60개 한도 안에서는 충분하다 |
| 7 | 프롬프트에 넣는 것은 **이름까지**: 위젯 종류·칸, 지표 이름·제목·단위·서비스, 패널·타일 제목, 서비스 ID, 서버 그룹, 서버 ID, 배치판, 지금 정의. 지표 값·로그는 넣지 않는다 | 밖의 LLM에 나가는 범위를 정해 둔다 |
| 8 | LLM 구현 `anthropic`(Claude, 공식 Go SDK)을 더한다. 기본 모델 `claude-opus-5-5`, 서버 쪽 거절 대체(fallbacks default) 켬. `openai`(OpenAI 호환 사내 엔드포인트)는 그대로 | 시연은 Claude. 설정 한 줄로 바꾼다(`llm.type`) |
| 9 | 권한 운영 이상(저장과 같음), `dashboards_readonly`·폴더 없음이면 403. 초안 요청은 감사 기록에 남기지 않는다(저장이 남는다), slog에 actor·횟수·오류 수만 | 플로우 초안과 같다 |

하지 않는 것: 초안 자동 저장, 대화(여러 번 주고받기), 바뀐 위젯만 받기, 스트리밍 표시, 초안 요청 감사 기록.

## 2. 계약 (N0, 커밋됨)

- `model.DashAssistReq{text, dash, prompt_only, yaml}` · `model.DashAssistResp{available, provider, message, system, prompt, yaml, dash, errors, explain, tries}` (`internal/model/dash.go` 끝)
- `dash.Deps.LLM`(nil이면 none) · `dash.Deps.Flows`(배치판) · `mountAssist`(지금은 501 자리) · 매니저 연결 · 역할표 `POST /api/v1/assist/dash` 운영
- `llm.SplitYAML(out) (yaml, explain)` — 플로우 초안에서 옮김(같은 규칙: ```yaml 블록, 없으면 전체 / 블록 밖 첫 줄이 설명)

응답 규칙(N-A가 지키고 N-B가 믿는 것):

| 경우 | 코드 | 본문 |
|---|---|---|
| 편집 꺼짐(`book.Editable()` 거짓) | 403 | `{error}` (crud.go의 `msgReadOnly`) |
| JSON 아님, `text`·`yaml` 둘 다 빔 | 400 | `{error}` |
| `prompt_only` | 200 | `available`·`provider`·`system`·`prompt`·`errors: []` (LLM이 없어도) |
| `yaml` 있음 | 200 | LLM을 부르지 않는다. `yaml`·`dash`(읽지 못하면 없음)·`errors` |
| LLM none | 200 | `available: false`·`provider: "none"`·`message: "LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"`·`errors: []` |
| LLM 오류 | 502 | `{error: "LLM 응답을 받지 못했습니다: …"}` |
| 초안 | 200 | `available: true`·`yaml`·`dash`·`errors`·`explain`·`tries` |

- `errors`는 늘 배열(빈 것 `[]`). 사람이 읽는 한 줄씩, 위젯이면 `위젯 <id>: …`, 할 일까지(예: `위젯 cpu: 지표 host.cpu.usage 없음 — 지표 목록의 이름으로 바꾸세요`).
- 응답 `dash.id`는 요청 `dash.id`(없으면 `""`). LLM이 id를 바꿔도 덮는다. `owner`·`updated_by`·`updated_at`은 비운다.

## 3. 레인

| 레인 | 등급 | 쓰는 경로 | 받는 것 |
|---|---|---|---|
| N-A 서버 | 상 | `internal/manager/dash/assist.go`(자리 교체), `assist_test.go`, 필요하면 `dash/assistref.go` | 계약, `metricList`, `query.MergedHosts`, `Panels`, `Flows` |
| N-B 화면 | 상 | `web/static/pages/dashedit.js`(AI 초안 부분), 그 CSS, `web/static/mock/dash.js`, `mock/auth.js` | 계약의 응답 규칙 |
| N-C LLM·문서 | 하 | `internal/manager/llm/anthropic.go`·테스트, `go.mod`, `deploy/manager*.yaml` 주석, 07·05 문서, 보고 | 오케스트레이터가 직접 |

### N-A 서버

1. 순서: 편집 꺼짐 → 본문(1MB) → 빈 요청 → `prompt_only` → `yaml` → LLM none → LLM 묻기(최대 2번) → 응답. 표 그대로.
2. 바탕 정의: 요청 `dash`(없으면 빈 보드). 고치기면 프롬프트에 지금 정의를 YAML로 넣는다(`yaml.v3` Marshal, owner·updated_* 뺌).
3. 프롬프트(System + Prompt). **같은 입력이면 같은 글**(정렬, 시각 없음):
   - System: 이음(E:UM) 사용자 보드 작성 도우미, 요청을 보드 정의 YAML **하나**로, 목록에 있는 이름만, 한국어.
   - 답 형식: ```yaml 블록 하나 + 블록 앞 한 줄 설명(무엇을 만들었고/바꿨는지).
   - 형식: 보드 칸(id·title·desc·range·group·widgets), 위젯 칸, 12칸 격자·순서 배치·폭 1~12·높이 1~8, 범위 우선순위, 한도(위젯 60·쿼리 8·제목 80자). `model/dash.go` 주석을 간추린다.
   - 위젯 15종: 종류마다 한 줄(뜻, 기본 크기 `model.WidgetDefaultSize`, 쓰는 칸).
   - 규칙: 업무 지표는 가능하면 `ref: {panel, tile}`(패널 판정 그대로), 시계열은 `queries`, 막대·표는 `by: host|owner`, 범위는 `scope.group`·`scope.service`, 고칠 때는 **요청한 것만** 바꾸고 다른 위젯의 id·순서·칸은 그대로, 새 위젯 id는 `[a-z0-9][a-z0-9_-]*`.
   - 예시 보드 하나(`dashboards/trading.yaml` 모양, 상수).
   - 목록: 지표(`이름 | 제목 | 단위 | 서비스`, `metricList` 순, 최대 400줄, 넘으면 `… 외 N개`), 업무 패널(`id · 제목 · 그룹 · 서비스` + 타일 `제목(지표, 단위)`), 서비스 ID, 서버 그룹(접두사 포함), 서버 ID, 배치판.
   - 사용자 요청 글.
   - 다시 물을 때: 같은 System, Prompt 끝에 `이전 답:` YAML과 `검사 오류:` 목록, "오류를 고친 정의 전체를 다시".
4. 읽기: `llm.SplitYAML` → `yaml.v3` Decoder `KnownFields(true)`로 `model.Dash`. 못 읽으면 `errors: ["YAML을 읽지 못했습니다: …"]`, `dash` 없음(다시 묻기 대상).
5. 검사: `ValidateDash`(id가 비면 `draft`로 검사) + 참조 검사(모두 모아 중복 없이, 최대 20줄):
   - 지표: `metricList` 이름에 있어야 함. 끝이 `*`면 그 접두사로 시작하는 이름이 하나라도.
   - `ref`·`scope.panel`·업무 흐름 상자 `panel`과 `in`·`out`·`stock`(빈 값·`none` 제외)·`edge` 타일.
   - `scope.service`: 패널 service ∪ live 주인 키의 서비스(알려진 것이 없으면 건너뜀). `scope.group`·보드 `group`: 서버 그룹 접두사. `scope.hosts`: 서버 ID. `scope.board`: 배치판 접두사.
6. LLM 요청: `MaxTokens 4000`, `Temperature 0.2`, 요청의 context(취소 따라감).
7. 테스트(가짜 LLM: `llm.LLM`을 구현해 받은 요청을 적고 정해 둔 답을 차례로 돌려줌): none / prompt_only(지표·패널 타일·지금 정의·System이 들어감, LLM 안 부름) / 정상 초안(errors `[]`, tries 1, id 유지) / 지어낸 지표 → 다시 물어 통과(tries 2, 두 번째 프롬프트에 오류 글) / 끝까지 틀림(errors 있음, dash 있음) / 붙여넣기(LLM 안 부름, 모르는 칸 오류) / 400·403·502.
8. 보고 10줄 이내: 바꾼 파일, 테스트 결과, 계약에서 정한 것.

### N-B 화면

1. 머리에 `AI 초안` 단추(`정의` 옆). `GET /assist`(`{available, provider}`)를 한 번 받아 둔다.
2. 모달(플로우 편집기 `openAssist`와 같은 모양·클래스): 입력칸 하나(새 보드 / 고치기에 따라 예시 placeholder), 주 단추 `초안 만들기`, 보조 `프롬프트 복사`·`답 붙여넣기`.
   - LLM 없음: `초안 만들기` 대신 ⓘ(`LLM 연동 전 — 프롬프트를 복사해 다른 LLM에 넣고 답을 붙여 넣으세요`). 안내 문장은 늘어놓지 않는다.
   - `답 붙여넣기`: 입력칸이 붙여넣기 모드(placeholder `LLM 답(YAML)`), 주 단추 `불러오기` → `{yaml}`.
   - `프롬프트 복사`: `{text, dash, prompt_only: true}` → `system`+빈 줄+`prompt`를 클립보드에(실패하면 그 글을 고를 수 있게 보임).
   - 기다리는 동안 단추를 끄고 `만드는 중… 12초`. 닫으면 요청을 버린다(AbortController가 있으면 쓰고, 없으면 결과를 무시).
3. 받으면(`dash` 있음): 바뀌기 전 정의를 두고, 정의를 갈아 끼우고(위젯 `scope` 정규화는 `render`와 같게), 저장 안 한 변경으로, 미리보기를 다시 그린다. 머리에 꼬리표 `초안 · 검토 뒤 저장`(설명은 ⓘ title), 바뀐 것 한 줄 `위젯 +2 −1 · 바뀜 3`(위젯 id로 견줌), `되돌리기`(바뀌기 전 정의로, 꼬리표 지움). `errors`는 편집기 문제 칸에 그대로(할 일까지 쓰여 옴). 저장되면 꼬리표를 지운다.
   - `dash`가 없으면 모달을 닫지 않고 오류를 모달 안에 보인다. 403·502는 `error` 그대로.
4. mock(`?mock=1`): `/assist`는 지금처럼(`llm=1`이면 available). `POST /assist/dash`: 빈 요청 400, `prompt_only` → 고정 System·Prompt(받은 text·위젯 수가 보이게), `yaml` → `widgets:`가 있으면 고정 초안·없으면 errors `["YAML을 읽지 못했습니다: widgets가 없습니다"]`, LLM 없음 → available false, `llm=1`이면 받은 정의 + 위젯 둘 더함(`ref` 매매체결 대기 큐 stat, 시세 지연 timeseries) + 첫 지표 위젯 제목 바꿈, `explain` 한 줄, `tries` 1. 1.5초 늦게. `mock/auth.js`의 운영 경로 목록에 `/assist/dash`.
5. 확인: mock에서 새 보드·기존 보드 각각 초안 → 미리보기 → 되돌리기 → 다시 초안 → 저장, 붙여넣기, 프롬프트 복사, LLM 없음, 조회 역할(단추 없음), 밝음·어두움, 390px. 콘솔 오류 0.
6. 보고 10줄 이내.

## 4. 결과

진행: N0 계약(타입·경로 자리·역할·`llm.SplitYAML`)을 커밋하고 N-A(서버)·N-B(화면)를 워크트리 두 개로 동시에 돌렸다. 그동안 N-C(anthropic 구현·설정·문서)는 오케스트레이터가 직접 했다.

| 확인 | 결과 |
|---|---|
| Go | `gofmt`·`go vet ./...`·`go test ./...` 통과. 새 테스트: 초안 10개(지시서 §3 N-A 7번 전부), anthropic 2개(헤더·본문·temperature 안 보냄·거절·잘림·401에 키 없음·설정 오류) |
| 실제 매니저 + 가짜 Claude 서버 | anthropic SDK 경로 전체: 첫 답의 지어낸 지표(`host.cpu.usage`)가 참조 검사에 걸림 → 한 번 다시 물어 통과(`tries 2`, `errors []`, id 유지). 다시 묻는 프롬프트에 이전 답·오류 글. 요청 본문 `model claude-opus-5-5`·`max_tokens 16000`·`fallbacks default`·`effort low`·temperature 없음 |
| 실제 매니저 API | `prompt_only` System 4.4천 자 + Prompt 5.5천 자(157줄, 새 데이터 기준), 붙여넣기 참조 오류 4종(지표·서비스·그룹·타일, 할 일까지), 조회 역할 403 |
| 실제 매니저 화면(CDP) | 기존 보드 trading: AI 초안 → 꼬리표·`위젯 +1 −9 · 바뀜 4` → 미리보기 10→2 → 되돌리기 10, 붙여넣기 오류가 문제 칸에. 콘솔 오류 0 |
| mock 화면(N-B) | 새·기존 보드 초안·되돌리기·저장, 붙여넣기, 프롬프트 복사, LLM 없음 ⓘ, 조회 역할, 502, 늦은 응답 버림, 밝음·어두움. 54개 확인, 콘솔 오류 0 |
| compose | 매니저에만 `EUM_LLM`·`ANTHROPIC_API_KEY`(띄우는 셸 값, 없으면 빈 값 → none) |

레인이 정한 것(카드에 없던 것):
- N-A: 알려진 이름이 하나도 없는 종류(지표·그룹·서버·배치판·서비스)는 참조 검사를 건너뜀(패널은 늘 검사). 그룹 목록 = 서버 그룹 ∪ 패널 그룹(접두사 포함). 붙여넣기도 `SplitYAML`을 거쳐 LLM 답 통째로 붙여도 됨. 형식·위젯 15종·규칙·예시는 System(바뀌지 않음), 목록·지금 정의·요청은 Prompt. 오류 20줄 넘으면 19줄 + `… 외 N개`. 두 번째 묻기가 LLM 오류면 502.
- N-B: 문제 칸을 보드 칸 아래에 두고 `위젯 <id>:` 줄을 누르면 그 위젯을 고름. 위젯이 없으면 `dash`를 보내지 않고 새 보드로 물음. 초안을 거듭 받아도 되돌리기는 첫 초안 전으로(저장 상태도). `바뀜`에 보드 칸 포함. 502 아래 할 일 한 줄. mock 붙여넣기 초안에 지어낸 지표 하나(검사 오류가 보이게).
- N-C: anthropic은 공식 Go SDK(`anthropic-sdk-go/v2` v2.9.0), Beta Messages + 서버 쪽 거절 대체(`fallbacks: default`). Opus 5.5는 샘플링 값을 받지 않아 temperature를 보내지 않고, 생각이 늘 켜져 있어 `max_tokens` 하한 16000. effort 기본 `low`(시연 대기), 설정으로 바꿈. 설정 `llm.type`은 `${EUM_LLM}`(비면 none).

남은 것:
- Claude API(`anthropic`)는 키가 없어 실제 호출은 못 해 봤다(가짜 Messages 서버로 SDK 경로까지). 켜는 법: `EUM_LLM=anthropic ANTHROPIC_API_KEY=… deploy/demo.sh up`.
- 추가(11-12, 사용자 요청 "API 키 없이 Claude CLI로"): LLM 구현 `claude-cli` — 이 PC에 로그인된 `claude -p`를 부른다(`--output-format json --tools "" --safe-mode --no-session-persistence --system-prompt … --model … --effort low`, 글은 표준 입력, 빈 임시 폴더, `CLAUDECODE` 뺌, 동시 2개, 기본 180s). `--bare`는 OAuth를 읽지 않아 쓰지 않는다. dev 모드(demo.sh 기본)는 설정 파일 없이 `EUM_LLM`(+`EUM_LLM_MODEL`·`EUM_LLM_EFFORT`·`EUM_LLM_URL`·`EUM_LLM_KEY`)으로 켠다: `EUM_LLM=claude-cli EUM_LLM_MODEL=claude-opus-5-5 deploy/demo.sh up`. compose 컨테이너 안 매니저는 호스트 CLI를 못 쓴다.
- **실제 Claude(CLI) 확인**: 기존 보드 trading 고치기("시세 지연 p99 시계열 추가, CPU 막대를 메모리로") 10초·한 번에 통과·요청한 두 가지만 바뀜(위젯 8개 그대로). 새 보드("지수산출 점검") 8초·한 번에 통과: 지수산출 패널 타일 ref 4·처리량/지연 시계열(scope.service index)·그룹 지수산출 서버별 CPU/메모리·서비스 로그.
- 인터넷이 없는 곳: Claude 호출은 502(`Claude에 닿지 못했습니다…`)로 끝나고 프롬프트 복사·붙여넣기는 된다. 빌드는 SDK 의존성 때문에 `go mod download`(또는 `go mod vendor`해 둔 소스)가 필요하다.
- 390px에서 머리 메뉴가 넘쳐 모달이 오른쪽으로 잘린다(전부터 있던 문제).
