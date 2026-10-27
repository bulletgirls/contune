# contune (Con-A-Tune)

Control-M(배치 스케줄링) + onTune(서버 관제) + 업무 모니터링을 하나로 묶은 **경량 통합 관제·배치 실행 도구**입니다.

핵심 아이디어: **잡 실행 ID(`exec_id`)로 배치 실행, 잡 프로세스 리소스, 호스트 지표, 업무 지표를 같은 키와 같은 시간축에 묶습니다.** 배치가 실패하거나 느려지면 화면 하나에서 클릭 한 번으로 원인을 찾을 수 있습니다.

## 문서

| 문서 | 내용 |
|---|---|
| [docs/01-design-review.md](docs/01-design-review.md) | 초기 조사 세션(Gemini) 점검: 유지할 것, 과한 것, 틀린 것, 빠진 것 |
| [docs/02-mvp-architecture.md](docs/02-mvp-architecture.md) | MVP 아키텍처: 기술 스택, 구조, API 계약, 확장 인터페이스, 데이터 모델, 상태 머신, 보안 |
| [docs/03-roadmap.md](docs/03-roadmap.md) | 단계별 구현 계획(M0~M4), 병렬 개발 규칙, 테스트 전략, 데모 시나리오 |

## 현재 상태

설계 단계입니다. 다음 작업은 **M0 Walking Skeleton**(에이전트 → 매니저 → SQLite → 브라우저로 CPU 1초 차트)입니다.
