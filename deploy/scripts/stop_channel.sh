#!/bin/sh
set -eu
pkill -TERM -f "$DEMO/bin/channel_svc" || true
