#!/bin/sh
# 지수산출 서비스(index_svc)를 새로 띄우고 바로 끝난다. 시세 저널(quotes)을 읽어 지수 저널(index)에 쓴다.
# 저널 폴더는 에이전트 변수 BUS. 작업 폴더의 rate(1초마다 다시 읽음): crash = 결함 스위치(비우면 정상).
set -eu
pkill -f "$DEMO/bin/index_svc" || true
mkdir -p "$BUS"
[ -f rate ] || : > rate
setsid nohup "$DEMO/bin/index_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/index_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
