# 이음 (E:UM)

> 저장소 이름은 contune(Con-A-Tune)이고, 제품 이름은 서류 심사 기획서에 쓴 **이음(E:UM)**, 실행 파일은 `eum`입니다.

배치 스케줄러, 서버 관제, 업무 지표 수집, **함수 단위 추적**을 한 제품으로 새로 만든 **경량 스케줄러·관제 통합 도구**입니다. 비싸고 거대한 상용 제품 없이, 운영에 실제로 쓰는 기능과 **데이터 주권**을 함께 갖는 것이 목표입니다.

핵심 아이디어: **스케줄러가 실행마다 실행 ID(`exec_id`)를 발급하고, 그 실행과 실행이 띄운 서비스 프로세스의 자원, CPU를 쓴 함수, 같은 시각의 서버 지표와 업무 지표를 같은 키와 같은 시간축으로 우리 DB에 쌓습니다.** 주문이 몰려 체결이 밀리면 업무 지표에서 그 서비스를 띄운 실행으로 바로 가고, 타임머신에서 평시(A)와 밀린 구간(B)을 골라 CPU를 쓴 함수까지 내려갑니다. 같은 데이터는 SQL과 CSV로 꺼내 통계나 머신러닝에 씁니다.

## 결정 사항

| 항목 | 결정 |
|---|---|
| 언어 | Go 단일 바이너리 `eum`(`manager`, `agent`, `dev`, `fleet`, `burn`, `sql`). 시연용 체결 서비스만 운영 환경과 같은 C |
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
| 운영 환경 | 업무는 온라인(체결) 중심, 배치는 개장 준비와 정산. 배치는 셸 스크립트, 스크립트가 띄우는 서비스는 C(자바 일부). 운영 서버는 perf 사용 가능 |
| 시연 | Docker Compose: 매니저 1 + 에이전트 3 + 모의 서버 40대, 플로우 39개. 장애 장면은 주문량 폭주로 체결 처리가 밀리는 상황 |

## 문서

| 문서 | 내용 |
|---|---|
| [docs/01-design-review.md](docs/01-design-review.md) | 초기 조사 세션(Gemini) 점검: 유지할 것, 과한 것, 틀린 것, 빠진 것 |
| [docs/02-mvp-architecture.md](docs/02-mvp-architecture.md) | **계약.** 기술 스택, 수집 통로, 서비스 프로세스 추적, 함수 단위 추적(실측 포함), API와 응답 형식, 매니저 설정, 인터페이스 6개, **조립 계약(§7.1)**, 데이터 모델, 상태 머신, 화면, 시연 환경 |
| [docs/03-roadmap.md](docs/03-roadmap.md) | 당일 계획: 시연 시나리오, 개념 완결 기준과 축소 순서, 일정, 저장소 구조, 테스트 전략, 리스크, 심사 예상 질문 |
| [docs/04-implementation-plan.md](docs/04-implementation-plan.md) | **구현 지시서:** 설계·기획서 리뷰 결과(바뀐 것, 메운 빈틈), **§1.4 카드 정리(11-09)**, 마일스톤 M0~M4, 운영 규칙, 통합 점검표 |
| [docs/05-supplement.md](docs/05-supplement.md) | **보충 계약(11-09, 02보다 우선):** 지표 카탈로그, 업무 로그, 알림, 장애 위험 지수, 운영자 조작, 편집기, 자연어 초안, API·설정·타입·DDL 추가, 화면 보충, **§16 보안 강화(11-10)** |
| [docs/cards/](docs/cards/README.md) | **작업카드 원본.** 공통 지시문과 소유 경로(README), 카드 파일 8개(K·A·B·C·R·D·E·X) |
| [pipeline_plan.md](pipeline_plan.md) | **카드 47장의 등급·우선 세션·선행** (파이프라인의 기준) |
| [pipeline/ORCHESTRATOR.md](pipeline/ORCHESTRATOR.md) | 자율 오케스트레이터 지시문. 도구: `pipeline/dag.py`(상태), `split_cards.py`(카드 → `specs/<ID>.md`), `setup_worktrees.sh` |
| [docs/submission/eum-proposal.pdf](docs/submission/eum-proposal.pdf) | 서류 심사 기획서 "이음"(외부 심사위원용, A4 가로 15쪽). 원본은 같은 폴더의 `eum-proposal.html` |

## 현재 상태

카드 47장이 모두 끝나 한 브랜치에 모였습니다(Z1 최종 통합, 2026-11-09). `gofmt -l .` 빈 결과, `go vet ./...`, `go test ./...` 27개 패키지 통과, 개념 완결 테스트 `go test -run TestConcept ./internal/e2e/` 통과(이 PC에서 perf 표본 포함).

**바로 해 보기 (Docker 없이):**

    deploy/demo.sh setup     # 체결 서비스·eum 빌드, eum seed로 7일 이력과 장애 6건
    deploy/demo.sh up        # eum dev --fleet: 매니저 + 에이전트 3 + 모의 서버 40 = 43대
    deploy/demo.sh warmup    # 보조 플로우 12개(ext-send-margin은 일부러 실패)
    deploy/demo.sh run       # market-open: load-equity attempt 2, start-matching이 체결 서비스 기동
    deploy/demo.sh surge     # 접수율 3000 → 4500 → 6000 → 1500
    deploy/demo.sh settle    # daily-settle(대기 큐가 남아 있으면 HELD)
    deploy/demo.sh train; deploy/demo.sh sql; deploy/demo.sh status; deploy/demo.sh down

화면은 `http://localhost:8080/`(실제 API), `http://localhost:8080/?mock=1`(mock). 시연 계정은 `admin/eum-admin`(관리), `operator/eum-operator`(운영), `viewer/eum-viewer`(조회)이고, `demo.sh`는 `EUM_USER`·`EUM_PASS`(기본 admin)로 로그인합니다. 운영 계정의 해시는 `printf '%s' '<비밀번호>' | eum passwd`로 만듭니다.

**되는 것 (dev 모드 리허설로 확인):** 서버 43대·그룹 8개, `ext-04` 무응답·`dw-02` 메모리 위험, 플로우 39개·배치판 6개, 재시도·선후행·서버 넘기·중복 실행 409, `start-matching` 뒤에도 같은 `exec_id`로 `job.*`·`biz.*`·함수 표본 수집, 폭주 전·밀린 구간의 `book_insert` 비중 약 4% → 74%, 리소스 게이트 HELD → 해제, 강제 OK·허용 목록 밖 명령 저장 거절, 업무 로그 `(W)`·알림, 위험 모델 학습(AUC 0.99)·`eum sql` 질의 3개·CSV/접힌 스택/학습 데이터 내보내기, 재시작 뒤 실행 기록 유지(sqlite). **보안(11-10, 별도 포트 리허설로 확인):** 로그인 없이 API 401, 조회 계정의 실행·내보내기 403, 운영 계정의 삭제 403, 다른 Origin 403, 운영자 조작 actor `user:operator`, 에이전트 허용 목록 밖 명령(`sh -c id`)은 실행하지 않고 FAILED, CSP를 건 실제 서버에서 화면 정상.

**안 되거나 확인 못 한 것:**
- compose 모드(`docker compose up`)는 이 PC에서 Docker 데몬이 돌지 않아 확인하지 못했습니다.
- 브라우저 렌더링(차트·플레임 그래프·다크 모드)과 `node --check`는 브라우저·node가 없어 확인하지 못했습니다. 모든 화면 파일은 200으로 나가고 상대 import는 모두 실제 파일을 가리킵니다.
- 체결 서비스의 폭주 강도는 CPU 속도에 달려 있습니다. 이 PC에서는 6000건/초에서 p99가 약 25ms, 대기 큐가 약 130건까지만 올라 `(E)` 로그와 위험 지수 경고·위험이 나오지 않았습니다. 시연 PC에서 `deploy/bin/matching_svc --stats`로 `COST_SCAN`을 맞춥니다(demo/README.md).
- `agent.profiler.cpu_pct`는 평시 약 1~2%, 폭주 중 약 4%(dwarf 호출 그래프의 perf script 비용)로 설계 목표 1% 미만보다 큽니다. 화면에 실제 값을 띄우는 것으로 대신합니다.
- 함수 스택은 `matching_svc;[unknown];main;handle_order;…`처럼 `main` 앞에 `[unknown]`(libc 시작 프레임)이 하나 끼어 있습니다.

**시연 전 사람이 할 일:**
- 함수 추적: `sudo sysctl -w kernel.perf_event_paranoid=1`(재부팅하면 되돌아가므로 시연 직전에 확인).
- compose 시연을 하려면 Docker 데몬을 켜고 `deploy/demo.sh` 앞에 `EUM_MODE=compose`. 데몬이 없으면 dev 모드로 같은 장면을 보입니다.
- 시연 PC에서 `surge`를 한 번 돌려 p99가 수백 ms까지 오르는지 보고, 모자라면 `COST_SCAN`을 올려 다시 빌드합니다.
