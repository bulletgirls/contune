#!/bin/sh
set -eu
pkill -TERM -f "$DEMO/bin/quote_svc" || true
