#!/usr/bin/env bash
# 시세 폭주 장면: demo.sh quote-surge(4000 → 6000 → 8000 → 2000, 20초 간격)를 돌리며 화면을 캡처한다.
#   capture/quote.sh <저장소 루트> <출력 폴더>
set -eu
REPO="$(cd "$1" && pwd)"; OUT="$(mkdir -p "$2" && cd "$2" && pwd)"
HERE="$(cd "$(dirname "$0")" && pwd)"
T0=$(date +%s%3N); echo "$T0" > "$OUT/quote_t0.txt"
(cd "$REPO" && deploy/demo.sh quote-surge > "$OUT/quote_surge.log" 2>&1) &
node "$HERE/timed.js" "$OUT" 2 1280x720 "$T0" \
  "50:q_biz1=#biz/quote" "57:q_bizflow=#dash/bizflow" "64:q_biz2=#biz/quote" "72:q_dashboard=#dashboard"
wait
