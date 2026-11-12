#!/bin/sh
# 시세 서비스(quote_svc)를 새로 띄우고 바로 끝난다. 체결 저널(fills)을 읽어 시세 저널(quotes)에 쓴다.
# 저널 폴더는 에이전트 변수 BUS. 작업 폴더의 rate(1초마다 다시 읽음)는 저널 없이 단독으로 돌 때의 수신율(건/초)이다.
set -eu
pkill -f "$DEMO/bin/quote_svc" || true
mkdir -p "$BUS"
[ -f rate ] || echo 2000 > rate
setsid nohup "$DEMO/bin/quote_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/quote_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
