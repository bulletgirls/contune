#!/bin/sh
set -eu
pkill -TERM -f "$DEMO/bin/stats_svc" || true
