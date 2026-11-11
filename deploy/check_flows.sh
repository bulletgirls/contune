#!/bin/sh
# 배치판별 플로우 파일 수(7·6·7·9·8·4)와 합계 41을(장중에 batch-job, 06) 확인한다.
set -eu
cd "$(dirname "$0")/.."
bad=0
total=0
for pair in 개장준비:7 장중:6 정산:7 대외송수신:9 정보계적재:8 주간월간:4; do
  b=${pair%%:*}; want=${pair##*:}
  got=$(find "flows/$b" -name '*.yaml' 2>/dev/null | wc -l)
  total=$((total + got))
  [ "$got" -eq "$want" ] || { echo "배치판 $b: $got개 (기대 $want)"; bad=1; }
done
[ "$total" -eq 41 ] || { echo "합계 $total (기대 41)"; bad=1; }
[ "$bad" -eq 0 ] && echo "OK: 플로우 $total개"
exit "$bad"
