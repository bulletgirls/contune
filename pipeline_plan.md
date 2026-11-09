# 파이프라인 계획

> 오케스트레이터와 `pipeline/dag.py`가 이 표를 읽는다. **표의 열 순서와 형식을 지킨다**(ID | 등급 | 세션 | 선행 | 카드 | 원본).
> - 등급: `상` → 서브에이전트 모델 `opus`, `하` → `sonnet`.
> - 세션: 우선 세션. 그 세션이 바쁘면 비어 있는 다른 세션에서 실행해도 된다(선행 태그를 병합하므로 어느 워크트리에서나 같은 결과). 워크트리는 `../wt-s1` ~ `../wt-s4`, 브랜치는 `s1` ~ `s4`.
> - 선행: 쉼표 목록. `ALL`은 나머지 카드 전부. 선행 카드가 모두 `done`(태그 `done-<ID>`가 있음)일 때 시작할 수 있다.
> - 카드 본문: `specs/<ID>.md`(`python3 pipeline/split_cards.py`가 `docs/cards/*.md`에서 만든다).
> - 해커톤 당일 시간표(03 §3, 04 §6)는 참고값이다. 이 파이프라인은 시간을 지키지 않고 의존만 지킨다.

| ID | 등급 | 세션 | 선행 | 카드 | 원본 |
|---|---|---|---|---|---|
| K0 | 하 | s1 | - | 저장소 초기화 | K-contract.md |
| K1 | 상 | s1 | K0 | 계약 코드와 패키지 골격 | K-contract.md |
| K2 | 상 | s1 | K1 | 걷는 뼈대(eum dev, 토큰, 스모크) | K-contract.md |
| A1 | 하 | s2 | K1 | 순수 파서 | A-agent-exec.md |
| A2 | 상 | s2 | K1 | Executor | A-agent-exec.md |
| A3 | 상 | s2 | A2 | 명령 루프·이벤트 큐 | A-agent-exec.md |
| A4 | 상 | s2 | A1,A3 | ProcessTracker | A-agent-exec.md |
| A5 | 상 | s2 | A1,A4 | Profiler(perf)·프로파일 루프 | A-agent-exec.md |
| A6 | 상 | s2 | A3 | 에이전트 강제 종료 | A-agent-exec.md |
| A7 | 하 | s2 | K2 | 업무 로그 수집기(logtail) | A-agent-exec.md |
| B1 | 하 | s3 | K1 | 플로우 로더·레지스트리 | B-scheduler.md |
| B2 | 상 | s1 | K1 | Decide·FlowRunStatus | B-scheduler.md |
| B3 | 하 | s3 | K1 | StateStore 스위트·메모리·SQLite | B-scheduler.md |
| B4 | 상 | s1 | B1,B2,B3 | 런너·명령·이벤트·수동 실행 | B-scheduler.md |
| B5 | 하 | s1 | B4 | 화면용 실행·플로우 API | B-scheduler.md |
| B6 | 상 | s1 | B4,C3 | 리소스 게이트 | B-scheduler.md |
| B7 | 상 | s1 | B4 | 운영자 조작 API | B-scheduler.md |
| B8 | 상 | s1 | B1,B4 | 플로우 편집 API | B-scheduler.md |
| B9 | 하 | s1 | B8,R1 | LLM 연결·자연어 초안 | B-scheduler.md |
| C1 | 하 | s3 | K1 | 수집기 sim·push | C-collect-data.md |
| C6 | 하 | s3 | K1 | 수집기 procfs 확장 지표 | C-collect-data.md |
| C2 | 하 | s3 | K2 | MetricStore 스위트·SQLite | C-collect-data.md |
| C3 | 하 | s3 | K2 | 전송기·수신·live | C-collect-data.md |
| C4 | 하 | s3 | C2,C3 | 조회 API | C-collect-data.md |
| C5 | 하 | s3 | C2,B3 | 열린 데이터 | C-collect-data.md |
| X1 | 하 | s3 | C4,B3,B2 | 알림 Watcher·webhook | R-alert-risk.md |
| R1 | 상 | s3 | C2,B3 | 장애 위험 지수 | R-alert-risk.md |
| D0 | 하 | s4 | K1 | 화면 mock 전체 | D-web.md |
| D1 | 하 | s4 | D0 | 화면 골격·공통 부품·트리 | D-web.md |
| D2 | 하 | s4 | D1 | 배치: 플로우 그래프·노드 조작 | D-web.md |
| D3 | 하 | s4 | D1 | 플레임 그래프 A·B | D-web.md |
| D4 | 하 | s4 | D1 | 관제 대시보드 | D-web.md |
| D5 | 하 | s4 | D3 | 타임머신 | D-web.md |
| D7 | 하 | s4 | D1 | 서버 탭 | D-web.md |
| D8 | 하 | s4 | D2 | 배치 편집기·자연어 | D-web.md |
| D9 | 하 | s4 | D1 | 업무 탭 | D-web.md |
| D10 | 하 | s4 | D4 | 알림·장애 위험·장애 기록 화면 | D-web.md |
| X2 | 하 | s4 | D5 | 함수 비교 표 | X-integration.md |
| D6 | 하 | s4 | D2,D3,D4,D5,D7,D8,D9,D10,X2,B5,B7,B8,B9,C4,C5,X1,R1 | 실제 API 연결 | D-web.md |
| E1 | 하 | s2 | K0 | 시연 체결 서비스 | E-demo.md |
| E2 | 하 | s2 | E1,K1 | 이미지·compose·설정 | E-demo.md |
| E3 | 하 | s2 | B1 | 플로우·패널·스크립트 | E-demo.md |
| E4 | 하 | s2 | E2,E3,C5 | demo.sh·질의 | E-demo.md |
| E5 | 하 | s3 | C2,B3 | 시연 이력 생성기 | E-demo.md |
| F1 | 하 | s2 | K2,C1 | eum fleet·burn | X-integration.md |
| I3 | 상 | s1 | A5,A6,A7,B5,B6,B7,B8,C4,C5,X1,R1,F1,E3 | 개념 완결 E2E | X-integration.md |
| Z1 | 상 | s1 | ALL | 최종 통합·점검 | X-integration.md |
