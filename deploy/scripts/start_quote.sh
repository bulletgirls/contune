#!/bin/sh
# 시세 서비스(quote_svc)를 새로 띄우고 바로 끝난다. 수신율은 작업 폴더의 rate(건/초, 1초마다 다시 읽음).
set -eu
pkill -f "$DEMO/bin/quote_svc" || true
[ -f rate ] || echo 2000 > rate
setsid nohup "$DEMO/bin/quote_svc" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/quote_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
