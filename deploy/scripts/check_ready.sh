#!/bin/sh
set -eu
sleep 3
pgrep -f "$DEMO/bin/matching_svc" >/dev/null
