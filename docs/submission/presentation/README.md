# 결과 발표 자료

결과 발표회용 자료의 원본입니다. 산출물은 한 단계 위 폴더에 있습니다.

| 파일 | 내용 |
|---|---|
| [`../eum-presentation.pptx`](../eum-presentation.pptx) | 편집용. 26장(본편 21 + 부록 5), 장마다 발표자 노트(말할 내용) |
| [`../eum-presentation.pdf`](../eum-presentation.pdf) | 같은 내용의 PDF |

## 구성

심사 기준(완성도 30 · 업무 활용성 30 · 개선 효과 30 · 발표 및 이해도 10)에 맞춰 묶었습니다. 본문 슬라이드 오른쪽 위 칩이 그 장이 답하는 기준입니다.

| # | 제목 | 기준 |
|---|---|---|
| 1 | 표지: 사건은 하나, 도구도 하나 | |
| 2 | 한 장 요약: 심사 기준 세 가지에 대한 답 | |
| 3 | 문제: 사건은 하나인데, 도구는 여럿 | |
| 4 | 해결: 번호 하나로 띄우고, 지켜보고, 돌아본다 | |
| 5 | 기획서에서 약속한 것은 모두 동작 | 완성도 |
| 6 | 기획서의 화면 시안이 실제 화면으로 | 완성도 |
| 7 | 시연: 여섯 장면(영상 자리) | 완성도 |
| 8 | 핵심 장면: 타임머신에서 함수까지 | 완성도 |
| 9 | 약속보다 더 만든 것 | 완성도 |
| 10 | 테스트와 재실행으로 확인 | 완성도 |
| 11 | 운영 환경의 조건을 그대로 | 업무 활용성 |
| 12 | 통제: 역할 · 허용 목록 · 감사 기록 | 업무 활용성 |
| 13 | 새 업무는 파일 하나로 | 업무 활용성 |
| 14 | 적용 순서: 관측 → 배치 병행 → 대체 범위 | 업무 활용성 |
| 15 | 장애 조사 5단계 → 2단계 | 개선 효과 |
| 16 | 구조와 비용 | 개선 효과 |
| 17 | 데이터: SQL 결과와 차트 | 개선 효과 |
| 18 | 효과 종합과 근거 | 개선 효과 |
| 19 | 만든 방법(AI) | |
| 20 | 한계와 다음 단계 | |
| 21 | 맺음 | |
| 22~26 | 부록: 예상 질문 2장, 시세 폭주, 화면 모음, 수치의 출처 | |

- **시연 영상**은 7번 장 다음에 넣거나 7번 장 위에 얹습니다. 7번 장의 여섯 화면은 영상이 없을 때의 대체 자료이기도 합니다.
- **시간이 짧으면** 6 · 9 · 13 · 16 · 19번을 숨기고 1 · 2 · 3 · 4 · 5 · 7(영상) · 8 · 10 · 11 · 12 · 14 · 15 · 17 · 18 · 20 · 21 순서로 갑니다.
- 14번 장의 적용 순서는 제안입니다. 실제 계획에 맞게 고쳐 씁니다.
- 표지에는 팀 · 발표자 이름을 넣지 않았습니다.

## 다시 만들기

    cd docs/submission/presentation
    npm install                 # pptxgenjs, sharp, react, react-dom, react-icons
    node build.js               # ../eum-presentation.pptx
    python3 tools/render.py ../eum-presentation.pptx ../eum-presentation.pdf   # LibreOffice 필요

| 고칠 것 | 어디 |
|---|---|
| 글 · 수치 · 발표자 노트 | `slides/01-intro.js` ~ `06-appendix.js` (슬라이드 순서대로) |
| 색 · 글꼴 · 레이아웃 · 공용 부품 | `lib.js` (색은 기획서 `eum-proposal.html`의 변수 그대로) |
| 화면 그림 | `assets/*.png` (아래 "화면 다시 찍기") |

글꼴은 PPTX에 **맑은 고딕**(고정폭은 Consolas)으로 지정했습니다. `tools/render.py`로 만든 PDF는 그 PC에 맑은 고딕이 없으면 대체 글꼴(이번에는 Noto Sans CJK KR)로 그려지므로, PowerPoint에서 연 모양과 줄바꿈이 조금 다를 수 있습니다. PowerPoint에서 직접 "PDF로 저장"하면 맑은 고딕 그대로 나옵니다.

## 화면 다시 찍기

화면은 모두 실제 실행 화면입니다(2026-11-12, `eum dev --fleet`, 서버 46대). 시연 PC에서 다시 찍으려면 Playwright가 필요합니다.

    deploy/demo.sh setup && deploy/demo.sh up && deploy/demo.sh warmup
    deploy/demo.sh run          # 실행 중 플로우 그래프: capture/shot.js … "flow_run2=#board/개장준비/market-open"
    deploy/demo.sh quote && deploy/demo.sh train
    # 평시 3분 뒤
    node capture/shot.js <원본 폴더> 2 1280x720 "n_dashboard=#dashboard" "n_biz=#biz/matching" …
    capture/surge.sh <저장소> <원본 폴더>         # 체결 폭주 + 시점별 캡처 + 정산 보류
    node capture/svc_tm.js <원본 폴더> tmL_match matching match-01 <from> <to> <a1> <a2> <b1> <b2>   # A·B 구간 끌기(epoch ms)
    capture/quote.sh <저장소> <원본 폴더>         # 시세 폭주
    deploy/demo.sh crash        # 종료 보고서: "eL_exit=#exit/<id>"
    node tools/prepare_assets.js <원본 폴더> <기획서 쪽 이미지 폴더>   # 잘라서 assets/로

- `tools/prepare_assets.js`의 `JOBS`가 원본 이름 → 자르는 위치 → `assets/` 이름의 표입니다. 기획서 쪽 이미지는 `pdftoppm -png -r 200 -f 8 -l 11 ../eum-proposal.pdf <폴더>/p`.
- 타임머신 구간: `from = T0 − 120초`, `to = T0 + 170초`, A = `T0 − 110초 ~ T0 − 20초`, B = `T0 + 60초 ~ T0 + 100초` (`T0`는 `surge_t0.txt`).
- 종료 보고서 화면에는 실행 파일의 절대 경로가 나옵니다. 경로가 보이지 않게 위쪽(제목 · 요약 · 원인)과 호출 스택만 잘라 썼습니다.
- 워드마크(`assets/wordmark-*.png`)는 `node tools/wordmark.js`(기획서와 같은 Hahmlet · IBM Plex Mono).
- `assets/`에는 자료에 쓰지 않은 여분 화면도 있습니다(알림 목록 `scr-alerts`, 정산 보류 `scr-settle-held`, 강제 OK `scr-forced`, 로그인 `scr-login`, 서비스 목록 `scr-svcs`, 평시 패널 `scr-biz-normal` 등). 슬라이드를 바꿀 때 씁니다.

## 수치의 출처

자료의 수치는 2026-11-12에 다시 실행해 잰 값입니다(부록 26번 장에 같은 표).

| 수치 | 값 | 어떻게 쟀나 |
|---|---|---|
| 테스트 | 883건 통과 · 실패 0 · 건너뜀 1(도우미 프로세스), 패키지 39개 | `go test ./... -v`의 통과 수. `gofmt -l .` 빈 결과, `go vet ./...` 통과 |
| 개념 완결 E2E | 9단계(기동 → 편집기 → 실행 → 조작 → 연결 → 로그 · 알림 → 함수 → 활용 → 지속) | `TestConcept`, perf 있음 |
| 체결 폭주 | 유입 1,500 → 6,000건/초, p99 최대 1,031ms, 대기 큐 최대 6,031건 | `demo.sh surge` 뒤 서비스 타임머신의 최대값 |
| 함수 비중 | `book_insert` 0.0% → 69.8% | A 11:13:55–11:15:24(표본 343), B 11:16:45–11:17:24(표본 2,069) |
| SQL | `book_insert` 평시 11.1% · 밀린 구간 69.1% | `demo.sh sql`의 `slow-functions.sql` |
| 시세 폭주 | 지연 p99 638ms, 수신 대기 4,089건, `rebuild_book` 0% → 21.2% | `demo.sh quote-surge`, 서비스 로그와 타임머신 |
| 알림 | 6,000건/초가 된 지 16초 뒤 첫 타일 알림, 51초 뒤 위험도 99 | 접수율 변경 11:16:25, 알림 11:16:41 · 11:17:16 (평가 주기 5초) |
| 정산 게이트 | 35초 보류 뒤 시작 | `daily-settle` 11:16:55 보류 → 11:17:30 시작 |
| 크래시 | 6초 뒤 원인 알림 | `demo.sh crash`: 종료 11:21:45 → 알림 11:21:51, `book_insert에서 NULL 역참조(주소 0x8)` |
| 권한 | 401 · 403 · 403 | 로그인 없이 `/hosts`, 조회 계정의 플로우 실행, 다른 Origin의 변경 요청. 운영 계정의 플로우 삭제도 403 |
| 화면 응답 | 0.13초 · 0.19초 | `capture/measure_path.js`: 타일 클릭 → 타임머신, 구간 끌기 → 함수 비교(5회 중앙값) |
| 자원 | 프로세스 메모리 122MB, 실행 파일 23.7MB | `eum dev`(매니저 1 + 에이전트 4 + 모의 42)의 RSS |
| 추적 부하 | 약 3% | 업무 패널의 프로파일링 오버헤드 타일(2.8~3.1%) |
| 코드 규모 | Go 33,249줄 + 테스트 26,025줄, 화면 JS 17,930줄 | `git ls-files`로 센 줄 수(vendor 제외) |

**읽을 때 주의할 점**

- 환경은 리눅스 4코어 컨테이너, perf 6.8입니다. 서버 46대 중 42대는 모의 서버이고 서버 자원은 모의 값, 부하는 합성 주문입니다.
- 체결 서비스의 부하 상수 `COST_SCAN`은 [`demo/README.md`](../../../demo/README.md)의 절차대로 이 환경 CPU에 맞췄습니다(23 → 16, 저장소 파일은 그대로 23). 다른 PC에서는 수치가 달라집니다. 시연 영상과 숫자를 맞추려면 그 PC에서 화면을 다시 찍고 `slides/*.js`의 숫자를 고칩니다.
- 이 환경에서는 root로 돌려 perf가 커널 프레임까지 모았습니다(평시 플레임 그래프의 `finish_task_switch` 등).
