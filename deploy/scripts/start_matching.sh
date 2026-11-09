#!/bin/sh
# 체결 서비스(matching_svc)를 새로 띄우고 바로 끝난다.
set -eu
pkill -f "$DEMO/bin/matching_svc" || true
[ -f rate ] || echo 1500 > rate
setsid nohup "$DEMO/bin/matching_svc" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/matching_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
