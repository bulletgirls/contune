#!/bin/sh
# 매매통계 서비스(stats_svc)를 새로 띄우고 바로 끝난다. 체결 저널(fills)을 읽어 통계를 반영한다.
# 저널 폴더는 에이전트 변수 BUS. PIN_CPU가 있으면 그 코어 하나에 묶는다(dev의 dw-01. compose는 컨테이너 한도로).
set -eu
pkill -f "$DEMO/bin/stats_svc" || true
mkdir -p "$BUS"
[ -f rate ] || : > rate
if [ -n "${PIN_CPU:-}" ]; then
  setsid nohup taskset -c "$PIN_CPU" "$DEMO/bin/stats_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/stats_svc.$EUM_EXEC_ID.log" 2>&1 &
else
  setsid nohup "$DEMO/bin/stats_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/stats_svc.$EUM_EXEC_ID.log" 2>&1 &
fi
exit 0
