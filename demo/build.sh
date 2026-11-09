#!/bin/sh
# 운영 빌드와 같은 조건: -O2, 프레임 포인터 옵션 없음, strip 안 함.
out="${1:-deploy/bin/matching_svc}"
mkdir -p "$(dirname "$out")"
gcc -O2 -Wall -o "$out" demo/matching_svc.c
