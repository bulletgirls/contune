#!/bin/sh
# 시연용 업무 프로세스(demo/*_svc.c 전부)를 운영 빌드와 같은 조건(-O2, 프레임 포인터 옵션 없음, strip 안 함)으로 만든다.
#   채널 channel_svc · 체결 matching_svc · 시세 quote_svc · 지수 index_svc · 분배 dist_svc · 통계 stats_svc (카드 V, 공통 demo/flow.h)
# 인자는 출력 폴더(기본 deploy/bin).
set -eu
dir="${1:-deploy/bin}"
mkdir -p "$dir"
for src in demo/*_svc.c; do
  svc=$(basename "$src" .c)
  gcc -O2 -Wall -o "$dir/$svc" "$src"
done
