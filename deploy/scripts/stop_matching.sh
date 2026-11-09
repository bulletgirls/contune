#!/bin/sh
set -eu
pkill -TERM -f "$DEMO/bin/matching_svc" || true
