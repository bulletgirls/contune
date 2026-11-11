#!/bin/sh
# 시연용 업무 프로세스 둘을 운영 빌드와 같은 조건(-O2, 프레임 포인터 옵션 없음, strip 안 함)으로 만든다.
#   matching_svc: 체결(match-01)   quote_svc: 시세(feed-01)
# 인자는 출력 폴더(기본 deploy/bin).
set -eu
dir="${1:-deploy/bin}"
mkdir -p "$dir"
for svc in matching_svc quote_svc; do
  gcc -O2 -Wall -o "$dir/$svc" "demo/$svc.c"
done
