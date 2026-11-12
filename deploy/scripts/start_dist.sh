#!/bin/sh
# 분배 서비스(dist_svc)를 새로 띄우고 바로 끝난다. 지수(index)·시세(quotes) 저널을 읽어 세션에 송출한다.
# 저널 폴더는 에이전트 변수 BUS. 작업 폴더의 rate는 결함 스위치 자리(지금은 비어 있다).
set -eu
pkill -f "$DEMO/bin/dist_svc" || true
mkdir -p "$BUS"
[ -f rate ] || : > rate
setsid nohup "$DEMO/bin/dist_svc" --bus "$BUS" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/dist_svc.$EUM_EXEC_ID.log" 2>&1 &
exit 0
