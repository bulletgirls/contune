#!/bin/sh
# 정보계 적재(dw-load-01 step-1, dw-01): CPU를 태우는 적재 프로세스 dw_load를 DW_LOAD_N개(기본 8) 띄우고 끝날 때까지 기다린다.
# 매매통계(stats_svc)와 같은 서버에서 CPU를 나눠 쓰게 해 통계 처리 적체를 만든다(카드 V §8 장면 B).
# eum 바이너리에 심볼릭 링크를 걸어 이름(comm)만 다른 프로세스로 만든다(start_job.sh 방식).
# setsid 없이 띄워 스텝과 같은 프로세스 그룹에 둔다: 화면의 강제 종료(프로세스 그룹에 SIGTERM → SIGKILL)가 자식까지 내린다.
# PIN_CPU가 있으면 stats_svc와 같은 코어에 묶는다(dev). compose는 컨테이너 CPU 한도를 같이 쓴다.
set -eu
n="${DW_LOAD_N:-8}"
dur="${DW_LOAD_DURATION:-90s}"
ln -sf "$(command -v eum)" ./dw_load
echo "(I) 정보계 적재 시작: 적재 프로세스 ${n}개, ${dur}"
i=0
while [ "$i" -lt "$n" ]; do
  if [ -n "${PIN_CPU:-}" ]; then
    taskset -c "$PIN_CPU" "$PWD/dw_load" burn --cpu 1 --duration "$dur" </dev/null &
  else
    "$PWD/dw_load" burn --cpu 1 --duration "$dur" </dev/null &
  fi
  i=$((i + 1))
done
wait
echo "(I) 정보계 적재 끝"
