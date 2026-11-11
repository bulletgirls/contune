#!/bin/sh
# BATCH_JOB(batch-job 플로우): 서버 프로세스 셋(svr_a·svr_b·svr_c)을 띄우고 바로 끝난다.
# eum 바이너리에 심볼릭 링크를 걸어 이름(comm)만 다른 프로세스로 만든다. 서비스 정의는 두지 않는다
# (자동 서비스 svr_a·svr_b·svr_c로 잡힌다). eum burn은 --cpu가 정수라 svr_c만 코어 둘을 쓴다.
set -eu
EUM="$(command -v eum)"
for n in svr_a svr_b svr_c; do
  pkill -x "$n" || true
  ln -sf "$EUM" "./$n"
done
setsid nohup ./svr_a burn --cpu 1 --duration 12h </dev/null >/dev/null 2>&1 &
setsid nohup ./svr_b burn --cpu 1 --duration 12h </dev/null >/dev/null 2>&1 &
setsid nohup ./svr_c burn --cpu 2 --duration 12h </dev/null >/dev/null 2>&1 &
exit 0
