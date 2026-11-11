#!/bin/sh
set -eu
sleep 3
pgrep -f "$DEMO/bin/quote_svc" >/dev/null
