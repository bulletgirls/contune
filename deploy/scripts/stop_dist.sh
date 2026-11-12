#!/bin/sh
set -eu
pkill -TERM -f "$DEMO/bin/dist_svc" || true
