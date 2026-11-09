---
name: orchestrate
description: 이음(E:UM) 카드 파이프라인을 자율 실행한다. pipeline_plan.md의 카드를 세션 워크트리 4개(../wt-s1~s4)에 서브에이전트로 병렬 배정하고 dag_state.json으로 진척을 관리해 모든 카드가 끝날 때까지 사람에게 묻지 않고 진행한다. "오케스트레이터 시작", "파이프라인 실행", "카드 진행 이어서" 같은 요청에 쓴다.
---

`/home/han/work/contune/pipeline/ORCHESTRATOR.md`를 끝까지 읽고 그대로 실행한다.

- 사람에게 중간 확인을 요청하지 않는다.
- 상태는 `python3 pipeline/dag.py …`로만 바꾼다(`dag_state.json`을 직접 고치지 않음).
- 서브에이전트 prompt는 `dag.py launch` 출력을 그대로 넘기고, 여러 카드를 한 응답에서 동시에 띄운다.
- 인자가 `fresh`이면 ORCHESTRATOR.md §1의 "처음부터 다시" 절차를 따른다. 그 밖에는 이어서 진행한다.
