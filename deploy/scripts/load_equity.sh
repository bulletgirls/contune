#!/bin/sh
# 첫 시도만 실패한다(표식은 작업 폴더).
set -eu
test -f .load.once || { touch .load.once; echo "(E) 원장 파일 수신 대기 초과"; exit 1; }
eum burn --cpu 1 --duration 15s
