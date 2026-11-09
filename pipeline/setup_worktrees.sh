#!/bin/sh
# 세션별 워크트리 ../wt-s1 ~ ../wt-s4 (브랜치 s1 ~ s4)를 준비한다. 여러 번 돌려도 안전하다.
# - 없으면 main에서 만든다(브랜치가 이미 있으면 그 브랜치로).
# - 있으면 main으로 빨리 감기를 시도한다(카드 작업이 쌓여 있으면 그대로 둔다).
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PARENT=$(dirname "$ROOT")
cd "$ROOT"
mkdir -p pipeline/logs
for n in 1 2 3 4; do
  wt="$PARENT/wt-s$n"
  br="s$n"
  if [ -d "$wt/.git" ] || [ -f "$wt/.git" ]; then
    if git -C "$wt" merge --ff-only main >/dev/null 2>&1; then
      echo "wt-s$n: main으로 맞춤"
    else
      echo "wt-s$n: 카드 작업이 있어 그대로 둠 ($(git -C "$wt" log -1 --format=%h))"
    fi
  elif git show-ref --verify --quiet "refs/heads/$br"; then
    git worktree add "$wt" "$br" >/dev/null
    echo "wt-s$n: 기존 브랜치 $br로 만듦"
  else
    git worktree add -b "$br" "$wt" main >/dev/null
    echo "wt-s$n: main에서 새로 만듦"
  fi
done
git worktree list
