#!/bin/sh
# 채널 서비스(channel_svc)를 새로 띄우고 바로 끝난다. 주문을 받아 주문 저널(orders)에 쓴다.
# 저널 폴더는 에이전트 변수 BUS. 작업 폴더의 rate(1초마다 다시 읽음): 정수 = 접수율(건/초), slow = 느려짐 스위치.
set -eu
pkill -f "$DEMO/bin/channel_svc" || true
mkdir -p "$BUS"
[ -f rate ] || echo 1500 > rate
setsid nohup "$DEMO/bin/channel_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/channel_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
