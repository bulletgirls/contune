#!/bin/sh
# 체결 서비스(matching_svc)를 새로 띄우고 바로 끝난다. 주문 저널(orders)을 읽어 체결 저널(fills)에 쓴다.
# 저널 폴더는 에이전트 변수 BUS. 작업 폴더의 rate(1초마다 다시 읽음): crash = 결함 스위치(접수율은 채널 gw-01의 rate).
set -eu
pkill -f "$DEMO/bin/matching_svc" || true
mkdir -p "$BUS"
[ -f rate ] || echo 1500 > rate
setsid nohup "$DEMO/bin/matching_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/matching_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
