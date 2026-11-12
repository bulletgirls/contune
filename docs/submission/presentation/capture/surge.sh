#!/usr/bin/env bash
# 체결 폭주 장면: demo.sh surge(3000 20초 → 4500 20초 → 6000 60초 → 1500)를 돌리며 정해진 시점에 화면을 캡처한다.
#   capture/surge.sh <저장소 루트> <출력 폴더>
# 시작 시각(epoch ms)을 <출력 폴더>/surge_t0.txt에 남긴다(타임머신 구간 계산에 쓴다).
# 70초에 daily-settle을 실행해 "체결 대기가 남은 동안 보류" 장면을 만든다.
set -eu
REPO="$(cd "$1" && pwd)"; OUT="$(mkdir -p "$2" && cd "$2" && pwd)"
HERE="$(cd "$(dirname "$0")" && pwd)"
T0=$(date +%s%3N); echo "$T0" > "$OUT/surge_t0.txt"
(cd "$REPO" && deploy/demo.sh surge > "$OUT/surge.log" 2>&1) &
(sleep 70; cd "$REPO" && deploy/demo.sh settle > "$OUT/settle.log" 2>&1) &
node "$HERE/timed.js" "$OUT" 2 1280x720 "$T0" \
  "30:p_biz_ramp=#biz/matching" "58:p_biz1=#biz/matching" "66:p_bizflow=#dash/bizflow" \
  "76:p_settle_held=#board/정산/daily-settle" "84:p_biz2=#biz/matching" "91:p_dashboard=#dashboard" \
  "97:p_alerts=#alerts" "104:p_dashboard2=#dashboard" "112:p_settle_after=#board/정산/daily-settle" \
  "120:p_trading=#dash/trading" "128:p_dashboard3=#dashboard"
wait
