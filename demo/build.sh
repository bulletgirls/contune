#!/bin/sh
# 시연용 업무 프로세스(demo/*_svc.c 전부)를 운영 빌드와 같은 조건(-O2, 프레임 포인터 옵션 없음, strip 안 함)으로 만든다.
#   채널 channel_svc · 체결 matching_svc · 시세 quote_svc · 지수 index_svc · 분배 dist_svc · 통계 stats_svc (카드 V, 공통 demo/flow.h)
# 인자는 출력 폴더(기본 deploy/bin).
#
# 빌드 뒤 <출력 폴더>/builds.json에 build-id → 커밋 표를 남긴다(카드 Q). 매니저(manager.yaml code.builds)가 죽은 실행 파일의
# build-id로 그때 커밋을 찾아 지금 읽는 코드와 견준다. readelf·git이 없거나 git 저장소가 아니면 조용히 건너뛴다.
# 예전 빌드의 줄도 남긴다(다시 빌드하기 전에 뜬 프로세스가 아직 돌 수 있다). 최근 200개까지.
set -eu
dir="${1:-deploy/bin}"
mkdir -p "$dir"
for src in demo/*_svc.c; do
  svc=$(basename "$src" .c)
  gcc -O2 -Wall -o "$dir/$svc" "$src"
done

write_builds() {
  command -v readelf >/dev/null 2>&1 || return 0
  command -v git >/dev/null 2>&1 || return 0
  commit=$(git rev-parse --verify -q HEAD 2>/dev/null) || return 0
  [ -n "$commit" ] || return 0
  dirty=false
  [ -z "$(git status --porcelain -- demo 2>/dev/null || true)" ] || dirty=true
  at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  out="$dir/builds.json"
  new="$out.new.$$"
  keys="$out.keys.$$"
  body="$out.body.$$"
  trap 'rm -f "$new" "$keys" "$body"' EXIT
  : >"$new"
  : >"$keys"
  for src in demo/*_svc.c; do
    svc=$(basename "$src" .c)
    id=$(readelf -n "$dir/$svc" 2>/dev/null | sed -n 's/^[[:space:]]*Build ID:[[:space:]]*\([0-9a-fA-F][0-9a-fA-F]*\).*$/\1/p' | head -n 1)
    [ -n "$id" ] || continue
    id=$(printf '%s' "$id" | tr 'A-F' 'a-f')
    printf '  "%s": {"commit": "%s", "dirty": %s, "exe": "%s", "built_at": "%s"}\n' "$id" "$commit" "$dirty" "$svc" "$at" >>"$new"
    printf '  "%s":\n' "$id" >>"$keys"
  done
  [ -s "$new" ] || return 0
  cp "$new" "$body"
  if [ -f "$out" ]; then
    # 예전 줄(이 스크립트가 쓴 한 줄 한 항목 모양)에서 이번에 다시 나온 build-id는 뺀다
    grep '^  "[0-9a-f][0-9a-f]*": {' "$out" 2>/dev/null | sed 's/,$//' | grep -v -F -f "$keys" >>"$body" || true
  fi
  {
    echo '{'
    head -n 200 "$body" | sed '$!s/$/,/'
    echo '}'
  } >"$new"
  mv "$new" "$out"
}
write_builds
