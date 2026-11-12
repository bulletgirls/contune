#!/bin/sh
set -eu
pkill -TERM -f "$DEMO/bin/index_svc" || true
