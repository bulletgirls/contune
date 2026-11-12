# 이음 (E:UM)

> 저장소 이름은 contune(Con-A-Tune)이고, 제품 이름은 서류 심사 기획서에 쓴 **이음(E:UM)**, 실행 파일은 `eum`입니다.

배치 스케줄러, 서버 관제, 업무 지표 수집, **함수 단위 추적**을 한 제품으로 새로 만든 **경량 스케줄러·관제 통합 도구**입니다. 비싸고 거대한 상용 제품 없이, 운영에 실제로 쓰는 기능과 **데이터 주권**을 함께 갖는 것이 목표입니다.

핵심 아이디어: **스케줄러가 실행마다 실행 ID(`exec_id`)를 발급하고, 그 실행과 실행이 띄운 서비스 프로세스의 자원, CPU를 쓴 함수, 같은 시각의 서버 지표와 업무 지표를 같은 키와 같은 시간축으로 우리 DB에 쌓습니다.** 주문이 몰려 체결이 밀리면 업무 지표에서 그 서비스를 띄운 실행으로 바로 가고, 타임머신에서 평시(A)와 밀린 구간(B)을 골라 CPU를 쓴 함수까지 내려갑니다. 같은 데이터는 SQL과 CSV로 꺼내 통계나 머신러닝에 씁니다.

## 결정 사항

| 항목 | 결정 |
|---|---|
| 언어 | Go 단일 바이너리 `eum`(`manager`, `agent`, `dev`, `fleet`, `burn`, `sql`). 시연용 업무 프로세스 여섯(채널·체결·시세·지수산출·분배·매매통계, `demo/*_svc.c`)만 운영 환경과 같은 C |
| 해커톤 | 하루, 2명. 코드는 당일 처음부터 작성(대회 규칙). 사전에는 설계 문서, 작업카드, AI 지시문만 준비. 본선은 외부망·상용 AI로 개발하고 사내 데이터·소스는 쓰지 않음 |
| 심사 | 서류: 창의성 30, 업무 도구 실용성 30, 구현 가능성 20, 기대 효과 20 |
| 산출물 | 새 스케줄러의 **개념 검증판**. 12:00에 1차 구조(시스템 정보 수집 + 작업 스케줄러), 15:00까지 개념 7개(스케줄, 원격 실행, 한 통로 수집, 실행 ID 연결, 함수 단위 추적, 데이터 주권, 보안 최소선)를 세 흐름(실행·연결·활용)으로 잇고 이후에는 넓히기만 함 |
| 함수 단위 추적 | Linux `perf`로 스크립트가 띄운 C 서비스 프로세스까지 실행 ID로 상시 표본 수집(초당 49회, 10초 구간), 플레임 그래프 A·B 비교. 실측: 프로파일러 CPU 약 0.6% |
| 플로우 그래프 | 갈래·합류·서버 넘기·재시도를 그리는 DAG 화면(핵심) |
| 데이터 주권 | 공개 스키마, 분석용 뷰 `v_samples`·`v_profiles`, `eum sql`(읽기 전용), CSV·접힌 스택 내보내기 |
| 설계 원칙 | SRP·OCP·LSP·DIP(인터페이스 6개 + 조립 계약), 테스트로 수정을 보호(계약 스위트, 표 테스트), 단순성(외부 의존성 4개, 빌드 단계 없는 화면) |
| AI | 개발 수단(오케스트레이터 1 + 워커 서브에이전트 4, 카드 47장). 제품 안에는 **장애 위험 지수**(로컬 로지스틱 회귀, 참고 지표)와 LLM 연결 자리만(05 §6, §9) |
| 보충 (11-09) | 프로토타입 시연 뒤: 서버 탭 5개 서브 탭(CPU·메모리·시스템·프로세스·IO)과 과거 구간, **업무 탭**(APM 정보, `(E)` 로그 수집과 알림), 화면에서 작업 구성(허용 목록 안에서 YAML 파일을 씀)·강제 OK·재실행·강제 종료, 자연어 작업 초안 입력, 간결한 문구 |
| 보안 (11-10) | **화면 로그인과 역할 3개**(조회·운영·관리), CSRF·보안 헤더, **감사 기록 조회 화면**(관리 계정, 실행 조작·로그인 실패까지 한 목록), **호스트별 에이전트 토큰**, **에이전트 쪽 명령 허용 목록**(매니저가 뚫려도 서버는 정해진 명령만), TLS. 인증 판단은 조립부 두 곳뿐(05 §16, 카드 [S](docs/cards/S-security.md)·[감사](docs/cards/S-audit.md)) |
| 업무 지표 붙이기 (11-10) | 운영자가 새 업무 지표(예: 1분 시세량)를 붙이는 길: 로그 수집 화면의 **숫자 뽑기**(로그 줄의 숫자 → `biz.*` 지표, 이동 창), 타일의 **하한·값 끊김·감시 시간·min**, **패널 다시 읽기**(재시작 없이). 05 §18, 카드 [M](docs/cards/M-biz-metric.md) |
| 운영 환경 | 업무는 온라인(체결) 중심, 배치는 개장 준비와 정산. 배치는 셸 스크립트, 스크립트가 띄우는 서비스는 C(자바 일부). 운영 서버는 perf 사용 가능 |
| 시연 | Docker Compose: 매니저 1 + 실제 에이전트 7 + 모의 서버 39대(서버 46대, 그룹 일곱: 채널·매매체결·매매통계·정보분배·지수산출·청산결제·rds. 업무 패널은 여섯), 플로우 41개(시세 `quote-open`, 서버 프로세스 셋 `batch-job` 포함). 장애 장면은 주문량 폭주로 체결 처리가 밀리는 상황과, **따로 도는 시세 서비스**에 시세가 몰려 수신 버퍼가 넘치고 시세가 늦는 상황(11-11) |
| 업무 프로세스 분리 (11-11) | 체결(match-01 `matching_svc`, `market-open`)과 시세(feed-01 `quote_svc`, `quote-open`)는 서버·push 포트(9101·9102)·로그·실행 ID·패널(`매매체결`·`시세`)이 따로다. 밀리는 원인도 함수도 달라 플레임 그래프가 서로 다르다(`book_insert`가 커짐 vs 평시에 없던 `recover_gap → rebuild_book`이 생김). 타임머신의 업무 트랙은 실행이 낸 지표(`biz.order.*`·`biz.quote.*`)를 따른다. [demo/README.md](demo/README.md) |
| 실제 업무 흐름 (11-12) | 업무 여섯(채널 → 매매체결 → 시세 → 지수산출 → 분배, 매매체결 → 매매통계) = 패널 여섯 = **실제 C 프로세스 여섯**. 채널이 번호(`seq`)와 접수 시각을 붙인 64바이트 레코드가 **저널 파일**(`demo/flow.h`)로 다음 업무에 가서, 못 읽은 양이 곧 적체이고 죽었다 살아나도 읽던 자리부터 따라잡는다. **RT**(체결 엔진이 받은 때 → 처리 끝)와 **TAT**(채널 접수 → 체결 처리 끝)를 나눠 TAT만 오르면 채널, 둘 다 오르면 체결. 매매통계 **처리 적체**(체결 저널에서 못 반영한 건수)는 정보계 적재(`dw_load`)와 코어 하나를 겨루면 쌓인다. 실제 서버 7대(gw-01·match-01·feed-01·idx-01·feed-02·dw-01·batch-01), 서버 자원은 compose에서 **cgroup 수집기**(컨테이너 한도 = 서버 용량). rds는 DB가 아니라 다른 업무가 쓰는 rule을 정의하는 업무라 흐름에서 뺐다. 카드 [V](docs/cards/V-realflow.md), [demo/README.md](demo/README.md) |
| 종료 보고서 (11-11) | 서비스가 **죽은 직접 원인**: 에이전트가 subreaper로 종료 상태(신호·core·종료 코드)를 받고, core를 서버에서 분석해(core는 밖으로 안 나감) 죽은 곳·호출 스택을, 매니저가 같은 시각의 근거(직전 프로파일·RSS·다른 실행·운영자 조작·커널 로그)를 묶어 규칙으로 원인을 가린다. 화면 `#exit/<id>`(호출 스택)과 목록 `#exits`(실행 메뉴, 서버 프로세스 탭·실행 이력에서도 들어감), 근거 묶음 내려받기·LLM 참고 초안. 05 §19, 카드 [T](docs/cards/T-exit-report.md) |
| 실행 · 프로세스 · 서비스 (11-11) | 관측의 주어를 **서비스(와 그 프로세스)**로, 실행 ID는 "누가 언제 띄웠나"를 답하는 출처로 나눴다. 서비스는 `services/*.yaml` 규칙(env·exe·comm·cmdline)이나 스텝이 끝난 뒤 남은 프로세스의 **자동 서비스**(실행 파일 이름). 서비스 몫의 자원(`svc.*`)·업무 지표·로그·프로파일은 주인 키 `svc:<서비스>@<서버>`로 쌓여 재기동을 넘어 한 화면(서비스 타임머신 `#svc/<id>`)에서 본다. 프로세스는 하나씩(서버 프로세스 탭, 실행의 기동한 프로세스, 플로우 그래프의 `starts` 칩). 이음 밖 프로세스는 에이전트 YAML `observe` 범위 안의 서비스 규칙에 맞는 것만. 서비스 미기동·선언 확인 알림. [06](docs/06-run-process-service.md), 카드 [P](docs/cards/P-service.md) |
| 사용자 보드 (11-11) | **관제는 그대로 두고, 사용자가 구성하는 보드를 그 옆에 둔다.** 보드 하나 = `dashboards/<id>.yaml`(파일이 기준, 화면 편집기가 그 파일을 씀, 운영 이상·감사 기록·rev 충돌 409). 위젯 15종 = 지표 5(값·시계열·막대·표·글: 매니저가 보드 단위로 한 번에 계산) + 운영 10(업무 지표·**업무 흐름**·실시간 이벤트·서버·서버 맵·금일 배치·시간대별 배치·장애 위험도·로그·서비스: 관제 렌더러를 그대로. 업무 흐름은 업무 패널 상자와 화살표, 예시 보드 `bizflow`). 범위는 위젯 > 보드 그룹 > 트리. **보드는 알림을 내지 않는다**(알림은 패널 타일, 위젯은 `ref`로 그 판정을 그대로). 템플릿 `ops`(관제와 같은 구성)·`panel:<id>`, 벽면 `?tv=1`. [07](docs/07-boards.md), 카드 [W](docs/cards/W-boards.md) |
| 관제 AI (11-12) | 관제 맨 위 **AI 상황 브리핑**과 알림에서 **원인 함수까지 따라가는 추적**(`internal/manager/brief`, 원격 `feature_llm_main`에서 병합). 근거(알림·위험도·서버·배치·장애, 추적은 서비스 → A·B 구간 → 함수 비교 → 호출 경로)는 이음이 결정론적으로 모으고 LLM은 그 근거로 문장만 쓴다. 줄마다 근거 칩, 답은 근거 ID 검사를 거치고, LLM이 없거나 답이 어긋나면 같은 근거의 규칙 문장을 보인다. `GET /brief`·`GET /alerts/{id}/trace`, 실시간 이벤트의 `✦ 원인`, 타임머신이 A·B 구간과 함수를 골라 둔 채로 열림. LLM은 다른 AI 기능과 같은 `EUM_LLM`(`claude-cli` 등). 05 §21 |

## 문서

| 문서 | 내용 |
|---|---|
| [docs/01-design-review.md](docs/01-design-review.md) | 초기 조사 세션(Gemini) 점검: 유지할 것, 과한 것, 틀린 것, 빠진 것 |
| [docs/02-mvp-architecture.md](docs/02-mvp-architecture.md) | **계약.** 기술 스택, 수집 통로, 서비스 프로세스 추적, 함수 단위 추적(실측 포함), API와 응답 형식, 매니저 설정, 인터페이스 6개, **조립 계약(§7.1)**, 데이터 모델, 상태 머신, 화면, 시연 환경 |
| [docs/03-roadmap.md](docs/03-roadmap.md) | 당일 계획: 시연 시나리오, 개념 완결 기준과 축소 순서, 일정, 저장소 구조, 테스트 전략, 리스크, 심사 예상 질문 |
| [docs/04-implementation-plan.md](docs/04-implementation-plan.md) | **구현 지시서:** 설계·기획서 리뷰 결과(바뀐 것, 메운 빈틈), **§1.4 카드 정리(11-09)**, 마일스톤 M0~M4, 운영 규칙, 통합 점검표 |
| [docs/05-supplement.md](docs/05-supplement.md) | **보충 계약(11-09, 02보다 우선):** 지표 카탈로그, 업무 로그, 알림, 장애 위험 지수, 운영자 조작, 편집기, 자연어 초안, API·설정·타입·DDL 추가, 화면 보충, **§16 보안 강화(11-10)** |
| [docs/07-boards.md](docs/07-boards.md) | **사용자 보드(11-11):** 왜 관제를 대체하지 않고 옆에 두는가(다른 제품 사례), 결정 12개(11: 목록 대신 고르기 칸, 12: 업무 흐름 위젯), 보드 YAML 모양, 결과(§4) |
| [docs/06-run-process-service.md](docs/06-run-process-service.md) | **실행 · 프로세스 · 서비스(11-11):** 개념 정리와 결과(§12). 화면이 배치에 묶인 지점(근거와 파일 위치), `BATCH_JOB → svr_a·b·c` 예, 개체 셋과 관계, 화면별 변화, 단계 P0~P3, 정할 것 |
| [docs/cards/](docs/cards/README.md) | **작업카드 원본.** 공통 지시문과 소유 경로(README), 카드 파일 8개(K·A·B·C·R·D·E·X)와 보충 카드(…·U 업무 흐름·V 실제 업무 흐름·N 보드 AI 초안·Q 종료 보고서 소스 근거) |
| [pipeline_plan.md](pipeline_plan.md) | **카드 47장의 등급·우선 세션·선행** (파이프라인의 기준) |
| [pipeline/ORCHESTRATOR.md](pipeline/ORCHESTRATOR.md) | 자율 오케스트레이터 지시문. 도구: `pipeline/dag.py`(상태), `split_cards.py`(카드 → `specs/<ID>.md`), `setup_worktrees.sh` |
| [docs/submission/eum-proposal.pdf](docs/submission/eum-proposal.pdf) | 서류 심사 기획서 "이음"(외부 심사위원용, A4 가로 15쪽). 원본은 같은 폴더의 `eum-proposal.html` |

## 현재 상태

카드 47장이 모두 끝나 한 브랜치에 모였습니다(Z1 최종 통합, 2026-11-09). `gofmt -l .` 빈 결과, `go vet ./...`, `go test ./...` 27개 패키지 통과, 개념 완결 테스트 `go test -run TestConcept ./internal/e2e/` 통과(이 PC에서 perf 표본 포함).

**바로 해 보기 (Docker 없이):**

    deploy/demo.sh setup     # 업무 프로그램 여섯과 eum 빌드, eum seed로 7일 이력과 장애 6건
    deploy/demo.sh up        # eum dev --fleet: 매니저 + 실제 에이전트 7 + 모의 서버 39 = 46대
    deploy/demo.sh warmup    # 보조 플로우 12개(ext-send-margin은 일부러 실패)
    deploy/demo.sh quote     # quote-open: 받는 쪽부터 분배(feed-02) → 지수산출(idx-01) → 시세(feed-01) 기동
    deploy/demo.sh run       # market-open: load-equity attempt 2, 통계(dw-01) → 체결(match-01) → 채널(gw-01) 기동
    deploy/demo.sh trace     # 가장 최근 TRACE 주문 하나의 경로: 채널 → 체결 → 통계·시세 → 지수 → 분배 (번호를 주면 그 주문)
    deploy/demo.sh count     # 최근 5분 채널 전달 · 체결 처리 · 시세 생성 · 분배 송출 · 통계 반영 건수
    deploy/demo.sh surge     # 채널(gw-01) 접수율 3000(20초) → 4500(20초) → 6000(60초) → 1500, 위험도가 오르려면 6000을 약 50초 유지
    deploy/demo.sh load      # 정보계 적재(dw-load-01)가 dw-01 한 코어에서 매매통계와 겨뤄 처리 적체 (끄기: 프로세스 탭에서 강제 종료)
    deploy/demo.sh index-crash   # 지수산출 SIGFPE로 죽음, 분배의 지수 수신만 끊김 (되돌리기: quote)
    deploy/demo.sh channel-slow  # 채널 세션 확인이 느려져 TAT만 오름 (되돌리기: channel-ok)
    deploy/demo.sh crash     # 체결 SIGSEGV (되돌리기: run)
    deploy/demo.sh quote-surge   # 시세 수신율 4000 → 6000 → 8000 → 2000 (단독 실행에서만 뜻이 있다, 아래 참고)
    deploy/demo.sh quote-rate 0  # 시세 끊김 → 1분 시세량 하한 위험(되돌리기: quote-rate 2000)
    deploy/demo.sh settle    # daily-settle(대기 큐가 남아 있으면 HELD)
    deploy/demo.sh train; deploy/demo.sh sql; deploy/demo.sh status; deploy/demo.sh down

화면은 `http://localhost:8080/`(실제 API), `http://localhost:8080/?mock=1`(mock). 시연 계정은 `admin/eum-admin`(관리), `operator/eum-operator`(운영), `viewer/eum-viewer`(조회)이고, `demo.sh`는 `EUM_USER`·`EUM_PASS`(기본 admin)로 로그인합니다. 운영 계정의 해시는 `printf '%s' '<비밀번호>' | eum passwd`로 만듭니다.

**LLM 켜기(관제 AI 브리핑·원인 추적 · 보드 AI 초안 · 종료 보고서 분석, 11-12):** 기본은 꺼짐(none)이고, 꺼져 있어도 프롬프트 복사와 답 붙여넣기는 됩니다.

    EUM_LLM=claude-cli EUM_LLM_MODEL=claude-opus-5-5 deploy/demo.sh up   # API 키 없이: 이 PC에 로그인된 Claude CLI(claude -p)
    EUM_LLM=anthropic ANTHROPIC_API_KEY=… deploy/demo.sh up              # Claude API

값을 `deploy/.env.local`(커밋하지 않는 파일)에 적어 두면 `demo.sh`가 읽습니다. 종료 보고서 분석 때 LLM에 코드를 보내는 범위는 `EUM_CODE_LLM=off|snippets|tools`(dev 모드는 비면 tools)입니다. compose는 매니저 컨테이너에 CLI가 없으므로 `EUM_CLAUDE_CLI=1`(이미지에 CLI)과 `CLAUDE_CODE_OAUTH_TOKEN`(`claude setup-token`)이 더 필요합니다.

**되는 것 (dev 모드 리허설로 확인):** 서버 43대·그룹 8개, `ext-04` 무응답·`dw-02` 메모리 위험, 플로우 39개·배치판 6개, 재시도·선후행·서버 넘기·중복 실행 409, `start-matching` 뒤에도 같은 `exec_id`로 `job.*`·`biz.*`·함수 표본 수집, 폭주 전·밀린 구간의 `book_insert` 비중 약 4% → 74%, 리소스 게이트 HELD → 해제, 강제 OK·허용 목록 밖 명령 저장 거절, 업무 로그 `(W)`·알림, 위험 모델 학습(AUC 0.99)·`eum sql` 질의 3개·CSV/접힌 스택/학습 데이터 내보내기, 재시작 뒤 실행 기록 유지(sqlite). **보안(11-10, 별도 포트 리허설로 확인):** 로그인 없이 API 401, 조회 계정의 실행·내보내기 403, 운영 계정의 삭제 403, 다른 Origin 403, 운영자 조작 actor `user:operator`, 에이전트 허용 목록 밖 명령(`sh -c id`)은 실행하지 않고 FAILED, CSP를 건 실제 서버에서 화면 정상. **감사 조회(11-10):** 운영 계정의 실행·재실행·강제 종료·강제 OK·알림 확인과 로그인 실패가 한 목록에, 조회·운영 계정은 403, CSV 수식 주입 방지. **장애 위험도(11-10, 별도 포트 리허설로 확인):** 지수 절편 보정으로 평시 0(평시 창 중 경고 0.07%), 요인마다 서버·평소 값·확인 질문, 요인 용인 → 미확인 지수만 내려가고 원래 지수는 그대로, 한도 초과 때 자동 해제와 알림 재발생, 재시작 뒤 용인 유지.

**실제 업무 흐름(11-12, dev 모드, 별도 포트 18790 리허설, 실제 바이너리):** 평시에는 패널 여섯 모두 약 1,500건/초(지수 송출 36건/초), 주문 → 분배 p99 약 15~35ms. `demo.sh trace`는 같은 주문 번호가 채널 0.6ms → 체결 1.3ms → 통계·시세 2.4ms → 지수 3.2ms → 분배 3.5ms로 지나가는 것을 보인다. `surge`(6,000건/초)에서는 체결 대기 큐 3,168건·RT p99 529ms·TAT p99 530ms(위험)이고 채널·시세·지수·분배·통계는 밀리지 않는다(대기 0~120건). `load`는 dw-01 한 코어에서 `dw_load` 8개와 `stats_svc`가 겨뤄 반영량이 약 1,050건/초로 떨어지고 처리 적체가 15초마다 5,651 → 11,738 → 18,499건으로 늘며, 프로세스 목록의 `dw_load` → 실행 `dw-load-01 › step-1` → 강제 종료 뒤 14초 안에 적체 0, 종료 보고서 `killed.eum`(user:admin)이 남는다. `index-crash`는 종료 보고서 `crash.signal` "recalc_index에서 0으로 나눔(SIGFPE)"과 함께 분배의 지수 수신만 0으로 만들고 시세 수신은 1,497건/초 그대로이며 `quote`로 다시 띄운다. `channel-slow`는 채널 전달 지연 p99 1,583ms·전달 대기 2,373건, 체결 RT p99 0.3ms 그대로에 TAT p99 1,651ms(TAT만 오르면 채널). `count`(최근 5분)는 채널 전달 803,669 · 체결 처리 805,158 · 시세 생성 802,757 · 분배 송출 816,388(시세 + 지수) · 통계 반영 805,156.

**실제 업무 흐름 compose 리허설(11-12, Docker 28.1 · cgroup v1, 컨테이너 9개 = 매니저 + 실제 서버 7 + 모의 39대):** `EUM_MODE=compose deploy/demo.sh up`으로 46대, `quote` → `run` 뒤 패널 여섯이 dev와 같은 값(약 1,500건/초)으로 찬다(저널은 공유 볼륨 `eum-bus`). 서버 자원은 컨테이너 값이다(cgroup 수집기: 평시 dw-01 CPU 약 32%, 메모리 512MB 중 8MB). `load` → dw-01 컨테이너 CPU 100%(위험), 처리 적체 20초마다 17,855 → 37,029건, 강제 종료 뒤 15초 안에 0. `surge` → 체결 대기 큐 14,010건·RT p99 2,345ms, 뒤 업무는 밀리지 않음. `index-crash` → 종료 보고서 `crash.signal`(컨테이너에는 gdb가 없어 함수 이름 없이 "index_svc에서 산술 오류(SIGFPE)"). 컨테이너 안 함수 표본도 서버마다 쌓인다(feed-02 `encode_msg`, dw-01 `update_ohlcv`, feed-01 `decode_packet`, match-01 `check_risk`, idx-01 `weight_sum`), 프로파일링 오버헤드 약 2%. **리허설에서 고친 것:** 매니저 데이터 볼륨 권한(컨테이너를 PC 사용자 uid로), perf 표본 버퍼 한도(`ulimits.memlock`), 이미지에 `addr2line`(binutils), 컨테이너 시간대, 이미지를 한 번만 빌드(서비스마다 빌드해 빌드 캐시가 23GB까지 불어 `/var`가 찼다)와 `.dockerignore`.

**보드 AI 초안 · 종료 보고서 소스 근거(11-12, 카드 V·N·Q를 합친 뒤 시연 포트 :8080, 새로 빌드한 실제 바이너리, 실제 Claude CLI 2.1.310 · `claude-opus-5-5`):** `gofmt -l .` 빈 결과, `go vet ./...`, `go test ./...` 35개 패키지 통과. `EUM_LLM=claude-cli`로 띄워 `quote` → `run` 뒤 패널 여섯이 약 1,500건/초, `trace`는 채널 0.09ms → 분배 3.6ms. **보드 AI 초안**(`POST /assist/dash`, 보드 편집기의 `AI 초안`): "지수산출 업무 점검 보드" 요청이 7초에 한 번에 검사를 통과했다(위젯 10개, 초안은 저장하지 않음). **종료 보고서**: `demo.sh crash` 뒤 보고서에 소스 토막 4곳(`book_insert` 등)과 버전 `일치`가 붙고, LLM 분석(도구 탐색)은 21초에 "접수율 파일이 결함 스위치 `fault_armed`를 켜서 NULL인 `cold_head[s]`를 읽음"과 코드 근거 3곳·고칠 방향을 냈다. `demo.sh run`으로 체결이 다시 뜬다. 이 통합 확인은 API로 했고, 화면은 카드 N·Q가 각각 실제 매니저 화면으로 확인했다([N-dash-assist.md](docs/cards/N-dash-assist.md) §4, [Q-code-evidence.md](docs/cards/Q-code-evidence.md) §4).

**관제 AI 병합(11-12, 원격 `feature_llm_main`을 main에 합친 뒤 별도 포트 18860, 새로 빌드한 실제 바이너리, 실제 Claude CLI 2.1.310 · `claude-opus-5-5`):** `EUM_LLM=claude-cli`로 띄워 `quote` → `run` → `surge`를 세 번 돌렸다. 새 업무 구성(카드 V)에서도 타일 알림 → 패널 `매매체결`의 서비스 → `svc:matching@match-01` → `book_insert` Self 비중 0.4% → 75.5%, 호출 경로 `handle_order › match_order › book_insert`, 같은 구간 주문 유입량 1,493 → 5,825 TPS(×3.9)까지 따라간다. 브리핑 머리글은 알림이 뜬 10초 안에 `매매체결 처리 포화(경고) — 원인 후보 book_insert`(규칙), 약 30초 뒤 `매매체결 응답 지연(위험) — 원인 후보 book_insert, 주문 유입 ×3.8`(AI). 화면(headless chrome, 콘솔 오류는 전부터 있던 favicon 404뿐): 브리핑 카드·근거 칩·원인 추적 서랍 6단계·`타임머신에서 A·B 비교`가 A·B 구간과 `book_insert`를 골라 둔 채로 열림. **합치며 정한 것:** 브랜치의 `llm: { type: claude-code }`와 `eum dev --llm`은 main의 `claude-cli`·`EUM_LLM`과 같은 일을 해 걷어냈다(`EUM_LLM`이 없으면 `demo.sh up`이 `unbound variable`로 멈추던 것도 같이 없어짐), `EUM_SURGE_PEAK`는 문서에만 있어 `surge`에 넣었다. **리허설에서 고친 것:** 늘 위험인 서버(dw-02·ext-04)가 있으면 새 업무 알림이 앞서 쓴 AI 문장에 10~20초 가리던 것(규칙 머리글이 바뀌면 규칙 문장을 바로), 알림을 못 읽으면 `전체 정상`이라고 쓰던 것, 범위(`g`)별 기억에 상한이 없던 것, `위험 지수 13 (ok)`·`오류 로그 오류 증가` 같은 말. **말투:** 화면에 걸리는 LLM 글(브리핑·원인 추적·종료 보고서 분석)에 공통 말투 안내 `llm.Style`을 붙여 "~한 것으로 보입니다" 대신 `book_insert Self 비중 0.4% → 75.5%, 호출 경로 …`처럼 쓴다(초안 설명 줄·위험 지수 근거도 한 줄).

**업무 프로세스 분리(11-11, 별도 포트 18095 리허설, 실제 perf):** 서버 43대(실제 4 + 모의 39), 플로우 40개. `quote-open` → feed-01에 `quote_svc`가 떠서 시세 패널 타일이 `start-quote` 실행 ID로, 체결 패널은 `start-matching` 실행 ID로 따로 이어짐. 평시 시세 플레임 그래프 `encode_quote` 51% · `decode_packet` 24% · `update_depth` 12% · `build_snapshot` 6%(체결은 `check_risk`·`price_cross` 중심). `quote-surge`의 8000건/초 구간: 시세 지연 p99 14 → 약 600ms, 시세 누락 초당 수천 건, `(E) GAP`·`(E) DELAY` 로그, 타일 위험 알림(시세 지연 p99·평균 지연·누락·수신 대기)과 해제, 플레임 그래프에 평시에 없던 `rebuild_book` 약 13~20% · `request_snapshot` 약 1~2%. 같은 시각 체결 p99는 최대 0.55ms로 그대로. `quote-rate 0` → 1분 만에 `1분 시세량` 하한 위험(`feed-01`). 타임머신은 실행이 낸 지표로 업무 트랙을 고른다(시세 실행이면 `시세 처리량·수신 대기·시세 지연`, 체결은 지금과 같음). 시세 실행의 A(평시)·B(8000) 비교는 `가장 늘어난 함수 rebuild_book 비중 0% → 13%`. 서비스를 띄우고 바로 끝나는 스텝은 구간을 고르기 전 그래프가 늘 비어 있었는데(스텝 길이 0초), 상주 프로세스가 살아 있으면 지금까지 보여 준다(`실행 전체(상주 프로세스 포함)`). `eum dev`만 다시 띄워도 두 서비스는 같은 실행 ID로 다시 잡힌다.

**종료 보고서(11-11, 별도 포트 18096 리허설, 실제 바이너리·perf·gdb):** `start-matching`이 `setsid nohup`으로 띄운 `matching_svc`의 부모가 eum(subreaper), core 한도 256MiB로 올라감. `demo.sh crash` → 1초 안에 SIGSEGV·core(23MB, 작업 폴더) → 보고서 `crash.null` **`book_insert에서 NULL 역참조(주소 0x8)`**, 호출 스택(gdb) `book_insert+0x153 ← match_order ← handle_order ← main`, 커널 로그 `segfault at 8`, 직전 60초 표본 1,081개의 73%가 `book_insert`, crit 알림. `demo.sh run`을 다시 하면 이전 서비스가 `pkill`(SIGTERM)을 받고 종료 코드 0으로 끝나 `killed.run`(같은 서버의 start-matching 시작 0초 뒤 정상 종료, 알림 없음), `kill -9` → `killed.other`(보낸 쪽 모름, warn). 화면 `#exit/<id>`·타임머신 띠를 Playwright로 찍어 확인(밝음·어두움, 콘솔 오류 0). 서비스가 스텝 뒤에 죽은 실행의 타임머신은 프로파일 기본 구간을 종료 시각까지 본다(`실행 전체(상주 프로세스 포함) 05:14:39–05:15:26 · 샘플 1,081`).

**실행 · 프로세스 · 서비스(11-11, 별도 포트 18097 리허설, 실제 바이너리):** `demo.sh batch`(`batch-job › start` = 06 §2.1의 BATCH_JOB)가 띄운 `svr_a`·`svr_b`·`svr_c`가 실행 화면·실행 이력·플로우 그래프에 기동한 프로세스로 보이고, 스텝이 끝나고 10초 뒤 자동 서비스 셋으로 따로 잰다(`svr_c` 약 190%). `demo.sh svr-crash` → `svr_b` 종료 보고서에 서비스 링크, 근거의 프로세스 지표가 `svr_b` 자신. 체결은 `services/체결.yaml` 규칙으로 처음부터 서비스 `matching`이고 업무 지표·타일·서비스 타임머신이 서비스 기준(시간축에 기동한 실행). `eum dev`를 다시 띄워도 떠 있던 서비스를 다시 잡는다. e2e `TestConcept`는 5단계를 실행 → 자동 서비스 의미로 고쳤다. 남은 것은 06 §12.

**사용자 보드(11-11, 별도 포트 18390 리허설, 실제 바이너리):** 계약(W0)을 먼저 커밋하고 7레인(W-A~G)을 두 물결로 병렬 진행해 병합했다. 예시 보드 셋(`ops-wall`·`trading`·`settle`)이 실제 매니저에서 그려지고, `demo.sh run`·`quote`·`surge` 동안 체결·시세 Ref 타일·처리량·시세 지연 시계열·서버별 CPU 막대·서비스 자원 표(`matching@match-01`·`quote@feed-01`)가 값으로 차며, 운영 벽면은 위험도 100·실시간 이벤트를 관제와 같은 모양으로 보인다. 편집기에서 템플릿 → 위젯 더하기 → 지표 고르기 → 끌어서 순서 → 저장, 409·삭제·조회 역할 안내를 mock·실제 둘 다 확인. 자세한 것은 07 §4.

**시연 전 최종 리허설(11-11 밤, 시연 포트 :8080, 새 시드 `.run/final`, 실제 바이너리·perf·gdb·systemd-coredump):** `demo.sh`의 전 장면과 역할·보안, `eum dev` 재시작, 실제 화면 30개(headless chrome, 콘솔 오류 0)를 돌렸다. 체결 폭주 A·B `book_insert` 2% → 73%, 시세 `rebuild_book` 0% → 11%, 정산 HELD → 해제, 종료 보고서 셋(`crash.null`은 이 PC의 `coredumpctl`에서 core를 꺼내 gdb로, `killed.run`, `killed.other`), 재시작 뒤 실행 이력·보고서·모델 유지와 서비스 다시 잡기. **리허설에서 고친 것:** 서비스 몫 데이터가 주인 키(`svc:<서비스>@<서버>`)로 쌓인 뒤에도 실행 ID로만 찾던 네 곳(종료 보고서의 직전 프로파일 근거, 실행 타임머신의 "상주 프로세스 포함" 프로파일이 샘플 0, `slow-functions.sql` 빈 결과, `demo.sh export <exec_id>`의 빈 프로파일·로그), `exit.error`가 같은 서버 다른 실행의 `(E)` 줄을 원인 제목으로 쓰던 것. **정한 것:** 프로파일링 오버헤드 경고 기준 1% → 5%(실제 2~4%라 10분에 28번 열고 닫혔음), `surge`의 6000건/초를 20초 → 60초(위험도는 최근 60초 특징이라 20초로는 0 그대로, 약 50초 유지하면 0 → 100).

**안 되거나 확인 못 한 것:**
- 장애 위험도(학습된 모델)는 폭주 중 "주의"를 거치지 않고 0에서 100으로 건너뜁니다(11-11 리허설, 6000건/초 약 50초 뒤). 좁은 화면(390px)에서는 위 메뉴가 넘칩니다.
- 종료 보고서: 에이전트가 띄우지 않았거나 에이전트 재시작 전부터 떠 있던 서비스는 부모가 아니라 종료 코드를 모른다(core·커널 로그·정황으로만). 탐색 주기(5초)보다 먼저 죽은 프로세스(예: 뜨자마자 `port in use`로 끝남)는 보고가 없다. systemd-coredump는 이 PC에 없어 가짜 `coredumpctl`로만 시험했다. LLM 진단은 Claude CLI(`claude-cli`)로 실제 확인했다(11-12). Claude API(`anthropic`)는 키가 없어 가짜 서버로만 시험했고, 컨테이너 안의 CLI 호출은 토큰이 없어 확인하지 못했다(사내 LLM 연결 전). 이 컨테이너의 `/usr/bin/perf`는 커널과 맞지 않아 리허설은 `linux-tools-6.8`의 perf를 PATH 앞에 두고 했다.
- 업무 흐름 리허설(11-12)의 알아 둘 것: 저널(`--bus`)로 띄우면 시세 수신이 체결 처리량을 따르므로 `quote-surge`·`quote-rate`는 단독 실행(`--bus` 없이)에서만 뜻이 있다. 시드 7일 이력에는 새 지표(`biz.channel.*`·`biz.index.*`·`biz.dist.*`·`biz.stats.*`·`tat_*`)가 없다.
- compose 모드는 11-12에 이 PC에서 확인했습니다(위 "compose 리허설"). 남은 것: 컨테이너에서는 core를 꺼내지 못하고 gdb가 없어 종료 보고서에 죽은 함수 이름이 없습니다. 화면(브라우저)으로는 compose 쪽을 보지 않았습니다.
- 브라우저 렌더링(차트·플레임 그래프·다크 모드)과 `node --check`는 브라우저·node가 없어 확인하지 못했습니다. 모든 화면 파일은 200으로 나가고 상대 import는 모두 실제 파일을 가리킵니다. (11-11: 다른 PC에서 `node --check` 전 파일 통과, 시세 타임머신은 Playwright로 실제 화면을 찍어 확인)
- 업무 패널의 `오류 로그 발생률` 타일은 서비스 로그가 10분 넘게 자라지 않거나 에이전트를 다시 띄운 뒤에는 값이 없어 `서비스 미기동`으로 보입니다(로그 수집은 10분 안에 자란 파일만 활동 중으로 봅니다). 체결·시세 모두 같습니다.
- 체결 서비스의 폭주 강도는 CPU 속도에 달려 있습니다. 이 PC에서는 6000건/초에서 p99가 약 25ms, 대기 큐가 약 130건까지만 올라 `(E)` 로그와 위험 지수 경고·위험이 나오지 않았습니다. 시연 PC에서 `deploy/bin/matching_svc --stats`로 `COST_SCAN`을 맞춥니다(demo/README.md).
- `agent.profiler.cpu_pct`는 평시 약 1~2%, 폭주 중 약 4%(dwarf 호출 그래프의 perf script 비용)로 설계 목표 1% 미만보다 큽니다. 화면에 실제 값을 띄우는 것으로 대신합니다.
- 함수 스택은 `matching_svc;[unknown];main;handle_order;…`처럼 `main` 앞에 `[unknown]`(libc 시작 프레임)이 하나 끼어 있습니다. glibc 2.34+의 `__libc_start_main_impl`은 11-11에 시작 프레임으로 지웁니다(`quote_svc;main;…`).

**시연 전 사람이 할 일:**
- 업무 서비스 정의 여섯(`services/*.yaml`)의 감시 시간은 평일 08:30-21:30입니다. 시연이 그 밖이면 서비스가 "시간 외"로 보이고 미기동 알림이 쉬므로, 필요하면 `active`를 시연 시간에 맞춥니다.
- 함수 추적: `sudo sysctl -w kernel.perf_event_paranoid=1`(재부팅하면 되돌아가므로 시연 직전에 확인).
- compose 시연을 하려면 Docker 데몬을 켜고 `deploy/demo.sh` 앞에 `EUM_MODE=compose`. 데몬이 없으면 dev 모드로 같은 장면을 보입니다.
- 관제 AI 브리핑은 관제 화면이 열려 있는 동안 상황이 바뀔 때마다(20초 간격 이상) 뒤에서 LLM을 부릅니다. 폭주 2분 30초에 브리핑 7번 안팎과 추적 초안이 나가므로, 과금되는 연결(`anthropic`)이나 한도가 빠듯한 구독이면 호출 수를 먼저 봅니다. 연달아 폭주를 돌리면 A(평소) 구간에 앞 폭주가 들어가 변화가 작게 나옵니다(59.7% → 76.7%).
- LLM(`claude-cli`)을 쓰려면 매니저를 띄우는 계정으로 `claude`가 로그인되어 있어야 합니다(`claude`를 실행해 `/login`). 인터넷이 없으면 호출은 502로 끝나므로 프롬프트 복사와 답 붙여넣기로 보입니다.
- 시연 PC에서 `surge`를 한 번 돌려 p99가 수백 ms까지 오르는지 보고, 모자라면 `COST_SCAN`을 올려 다시 빌드합니다.
- 시세는 시작할 때 CPU 속도를 재 비용을 맞추므로 다시 빌드할 일이 적습니다. `quote-surge` 8000 구간에서 `(E) GAP`이 나오는지만 봅니다.
