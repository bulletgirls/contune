#!/bin/sh
# UI 리뷰용 미리보기 빌드. 작업본 정적 파일을 .run/ui-preview/로 복사하고
# mock 모드로 고정한다(백엔드·?mock=1 없이 뜬다). 저장소 원본은 건드리지 않는다.
# 산출물은 claude.ai 아티팩트로 그대로 올린다:
#   페이지   .run/ui-preview/index.html
#   files 맵 .run/ui-preview.files.json (index.html을 뺀 나머지 파일)
set -eu
cd "$(dirname "$0")/.."
src=internal/manager/web/static
out=.run/ui-preview
map=.run/ui-preview.files.json

rm -rf "$out"
mkdir -p "$out"
cp -R "$src"/. "$out"/

# isMock()이 항상 true를 돌려주게 한다.
sed -i 's|^export function isMock() {$|&\n  return true; // 미리보기 빌드: 항상 mock|' "$out/api.js"
grep -q '미리보기 빌드: 항상 mock' "$out/api.js" || {
  echo "api.js에서 'export function isMock() {' 줄을 찾지 못함" >&2
  exit 1
}

# 확장자로 형식을 알 수 없는 파일(vendor/LICENSE 등)은 text/plain으로 올린다.
(cd "$out" && find . -type f ! -path ./index.html | sed 's|^\./||' | sort) | awk '
  BEGIN { printf "{" }
  {
    if (NR > 1) printf ", "
    if ($0 ~ /\.(js|css|json|svg|png|jpg|gif|webp|woff2?|ttf)$/) printf "\"%s\": \"%s\"", $0, $0
    else printf "\"%s\": {\"from\": \"%s\", \"contentType\": \"text/plain\"}", $0, $0
  }
  END { print "}" }' > "$map"

n=$(find "$out" -type f | wc -l)
echo "OK: $out (파일 ${n}개, mock 고정), files 맵 $map"
