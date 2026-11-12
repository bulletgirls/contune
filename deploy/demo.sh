#!/usr/bin/env bash
# 시연 진행 스크립트: deploy/demo.sh <setup|up|down|warmup|run|surge|crash|quote|quote-surge|quote-rate|batch|svr-crash|settle|train|sql|export|status>
# 업무 프로세스 둘: 체결(match-01 matching_svc, run·surge), 시세(feed-01 quote_svc, quote·quote-surge·quote-rate)
# 환경변수: EUM_URL(기본 http://localhost:8080) EUM_MODE(dev|compose, 기본 dev) EUM_DATA(기본 ./data-dev)
#          EUM_COMPOSE(compose 파일, 기본 deploy/docker-compose.yml) EUM_DEV_FLAGS(eum dev에 더 넘길 플래그)
#          EUM_HOSTS(up이 기다리는 호스트 수, 기본 43) EUM_BIN(eum 실행 파일, 기본 deploy/bin/eum) EUM_TOKEN(기본 dev)
#          EUM_USER·EUM_PASS(화면 로그인 계정, 기본 admin/eum-admin. 로그인이 꺼져 있으면 쓰지 않음)
#          EUM_SURGE_PEAK(surge가 6000건/초를 유지하는 초, 기본 60)
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

EUM_URL="${EUM_URL:-http://localhost:8080}"
EUM_MODE="${EUM_MODE:-dev}"
EUM_DATA="${EUM_DATA:-./data-dev}"
EUM_COMPOSE="${EUM_COMPOSE:-deploy/docker-compose.yml}"
EUM_DEV_FLAGS="${EUM_DEV_FLAGS:-}"
EUM_HOSTS="${EUM_HOSTS:-43}"   # up이 기다리는 호스트 수
EUM_BIN="${EUM_BIN:-deploy/bin/eum}"
export EUM_TOKEN="${EUM_TOKEN:-dev}"
export EUM_USER="${EUM_USER:-admin}"
export EUM_PASS="${EUM_PASS:-eum-admin}"
API="$EUM_URL/api/v1"
RUN="$EUM_DATA"
COOKIES="$RUN/demo.cookies"   # 화면 로그인 세션(API를 부르는 curl이 모두 같이 쓴다)
Q=deploy/queries

# 보조 플로우 12개(ext-send-margin은 일부러 실패해 "실패 1"을 만든다)
WARMUP_FLOWS="ext-send-margin ext-send-bank ext-send-sec ext-recv-bank ext-recv-kftc intra-feed-01 intra-gw-01 dw-load-01 dw-load-02 open-ref-01 settle-ref-01 weekly-ops-01"

say() { echo "[demo:$1] $2"; }
die() { echo "demo.sh: $*" >&2; exit 1; }
compose() { docker compose -f "$EUM_COMPOSE" "$@"; }
# acurl: 세션 쿠키를 읽고 쓰는 curl. API를 부르는 curl은 모두 이것을 쓴다.
acurl() { curl -b "$COOKIES" -c "$COOKIES" "$@"; }
get() { acurl -fsS "$API/$1"; }
post() { acurl -sS -o /dev/null -w '%{http_code}' -X POST "$API/$1"; }

# login: 화면 로그인이 켜져 있으면 EUM_USER로 한 번 로그인해 쿠키를 남긴다.
# 꺼져 있거나(/me의 auth:false) 쿠키가 아직 유효하면(auth:true) 건너뛴다. 비밀번호는 명령행에 싣지 않는다(표준 입력).
login() {
  mkdir -p "$RUN"
  [ -f "$COOKIES" ] || (umask 077; : > "$COOKIES")
  me=$(acurl -sS "$API/me" 2>/dev/null) || return 1
  case "$me" in *'"auth":false'*|*'"auth":true'*) return 0 ;; esac
  code=$(python3 -c 'import json,os; print(json.dumps({"id":os.environ["EUM_USER"],"password":os.environ["EUM_PASS"]}))' |
    acurl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data-binary @- "$API/login") || return 1
  [ "$code" = 200 ] || { echo "demo.sh: 로그인 실패(HTTP $code, 계정 $EUM_USER)" >&2; return 1; }
}
need_login() { login || die "화면 로그인을 하지 못했습니다($EUM_URL, EUM_USER·EUM_PASS 확인)"; }
# jget <경로> <python 식>: JSON 응답 d에 식을 적용해 출력
jget() { get "$1" | python3 -c 'import sys,json; d=json.load(sys.stdin); print('"$2"')'; }

# write_rate <호스트> <건/초>: 업무 프로세스가 1초마다 다시 읽는 작업 폴더의 rate 파일에 쓴다.
write_rate() {
  if [ "$EUM_MODE" = compose ]; then
    compose exec -T "$1" sh -c "echo $2 > /var/lib/eum/work/rate"
  else
    mkdir -p "$EUM_DATA/work/$1"
    echo "$2" > "$EUM_DATA/work/$1/rate"
  fi
}
set_rate() { write_rate match-01 "$1"; }    # 체결 서비스 접수율
quote_rate() { write_rate feed-01 "$1"; }   # 시세 서비스 수신율

host_count() { get hosts 2>/dev/null | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["hosts"]))' 2>/dev/null || echo 0; }

cmd_setup() {
  if [ "$EUM_MODE" = compose ]; then
    say setup "compose 모드는 준비할 것이 없습니다(up이 이미지를 빌드)"
    return
  fi
  say setup "matching_svc·quote_svc 빌드, eum 빌드, 7일치 이력 시드 (장면 0)"
  sh demo/build.sh
  mkdir -p "$(dirname "$EUM_BIN")"
  go build -o "$EUM_BIN" ./cmd/eum
  "$EUM_BIN" seed --data "$EUM_DATA" --days 7 || say setup "seed 실패(E5 전이면 정상): 계속합니다"
}

cmd_up() {
  say up "시연 환경을 띄우고 /hosts가 43대가 될 때까지 기다립니다 (장면 2)"
  if [ "$EUM_MODE" = compose ]; then
    compose up -d --build
  else
    [ -x "$EUM_BIN" ] || go build -o "$EUM_BIN" ./cmd/eum
    mkdir -p "$EUM_DATA"
    # shellcheck disable=SC2086
    nohup "$EUM_BIN" dev --fleet deploy/fleet.yaml --data "$EUM_DATA" $EUM_DEV_FLAGS \
      > "$EUM_DATA/dev.log" 2>&1 < /dev/null &
    echo $! > "$EUM_DATA/dev.pid"
  fi
  n=0
  for _ in $(seq 1 60); do
    n=0
    login 2>/dev/null && n=$(host_count)
    [ "$n" -ge "$EUM_HOSTS" ] && { say up "호스트 $n대 확인"; return; }
    sleep 1
  done
  die "60초 안에 호스트 ${EUM_HOSTS}대가 되지 않았습니다(지금 ${n}대)"
}

cmd_down() {
  say down "시연 환경을 끕니다"
  if [ "$EUM_MODE" = compose ]; then
    compose down
    return
  fi
  if [ -f "$EUM_DATA/dev.pid" ]; then
    pid=$(cat "$EUM_DATA/dev.pid")
    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
    kill -9 "$pid" 2>/dev/null || true
    rm -f "$EUM_DATA/dev.pid"
  fi
  pkill -f "$ROOT/deploy/bin/matching_svc" 2>/dev/null || true
  pkill -f "$ROOT/deploy/bin/quote_svc" 2>/dev/null || true
  return 0
}

cmd_warmup() {
  say warmup "보조 플로우 12개를 수동 실행해 배치판 진행과 실패 1을 채웁니다 (장면 2)"
  for f in $WARMUP_FLOWS; do
    code=$(post "flows/$f/run")
    echo "  $f -> HTTP $code"
  done
}

cmd_run() {
  say run "load-equity 재료를 지우고 market-open을 실행합니다 (장면 3)"
  if [ "$EUM_MODE" = compose ]; then
    compose exec -T batch-01 rm -f /var/lib/eum/work/.load.once
  else
    rm -f "$EUM_DATA/work/batch-01/.load.once"
  fi
  set_rate 1500    # crash 스위치가 남았으면 걷어 정상으로 띄운다
  code=$(post "flows/market-open/run")
  echo "  market-open -> HTTP $code"
  [ "$code" = 201 ] || die "market-open 실행 실패(HTTP $code)"
}

# 6000은 EUM_SURGE_PEAK초(기본 60) 유지한다: 빠른 PC에서는 6000을 15~40초 받아야 처리가 밀려 체결 알림(p99·대기 큐)이 뜬다.
cmd_surge() {
  peak="${EUM_SURGE_PEAK:-60}"
  case "$peak" in *[!0-9]*|'') die "EUM_SURGE_PEAK는 초(정수): '$peak'" ;; esac
  say surge "접수율 3000 -> 4500 -> 6000(${peak}초 유지) -> 1500, 20초 간격 (장면 5)"
  for r in 3000 4500 6000 1500; do
    set_rate "$r"
    echo "  $(date +%H:%M:%S) 접수율 $r"
    case "$r" in
      1500) ;;
      6000) sleep "$peak" ;;
      *) sleep 20 ;;
    esac
  done
}

cmd_crash() {
  say crash "체결 서비스 버그 스위치를 켭니다(접수율 파일에 crash)"
  write_rate match-01 crash
  echo "  몇 초 안에 체결 서비스가 죽고 종료 보고서가 생깁니다(#exit). 다시 띄우려면 demo.sh run"
}

# 시세: 체결과 따로 도는 업무 프로세스(feed-01 quote_svc). 처리 한계는 약 6,000건/초(시작할 때 CPU 속도를 재 맞춘다).
cmd_quote() {
  say quote "quote-open을 실행해 feed-01에 시세 서비스를 띄웁니다(수신율 2000건/초)"
  quote_rate 2000
  code=$(post "flows/quote-open/run")
  echo "  quote-open -> HTTP $code"
  [ "$code" = 201 ] || die "quote-open 실행 실패(HTTP $code)"
}

cmd_quote_surge() {
  say quote-surge "시세 수신율 4000 -> 6000 -> 8000 -> 2000, 20초 간격 (시세 지연 장면)"
  for r in 4000 6000 8000 2000; do
    quote_rate "$r"
    echo "  $(date +%H:%M:%S) 시세 수신율 $r"
    [ "$r" = 2000 ] || sleep 20
  done
}

cmd_quote_rate() {
  [ $# -ge 1 ] || die "사용법: demo.sh quote-rate <건/초>  (0이면 시세 끊김)"
  case "$1" in *[!0-9]*|'') die "quote-rate는 0 이상의 정수: '$1'" ;; esac
  quote_rate "$1"
  say quote-rate "시세 수신율 $1건/초"
}

# BATCH_JOB: batch-01에 서버 프로세스 셋(svr_a·svr_b·svr_c)을 띄운다(수동 실행, 자동 서비스로 잡힌다).
cmd_batch() {
  say batch "batch-job을 실행해 batch-01에 svr_a·svr_b·svr_c를 띄웁니다"
  code=$(post "flows/batch-job/run")
  echo "  batch-job -> HTTP $code"
  [ "$code" = 201 ] || die "batch-job 실행 실패(HTTP $code)"
}

# svr-crash: svr_b만 SIGSEGV로 죽인다(종료 보고서 장면). 다시 띄우려면 demo.sh batch.
cmd_svr_crash() {
  say svr-crash "svr_b를 SIGSEGV로 죽입니다"
  if [ "$EUM_MODE" = compose ]; then
    compose exec -T batch-01 pkill -SEGV -x svr_b
  else
    pkill -SEGV -x svr_b || die "svr_b가 떠 있지 않습니다(demo.sh batch 먼저)"
  fi
  echo "  몇 초 안에 종료 보고서가 생깁니다(#exit)"
}

cmd_settle() {
  say settle "daily-settle 실행(게이트 장면)"
  code=$(post "flows/daily-settle/run")
  echo "  daily-settle -> HTTP $code"
  [ "$code" = 201 ] || die "daily-settle 실행 실패(HTTP $code)"
}

cmd_train() {
  say train "위험 모델을 지금 학습합니다 (장면 7)"
  acurl -fsS -X POST "$API/risk/train" | python3 -c '
import sys,json
d=json.load(sys.stdin)
print(json.dumps(d, ensure_ascii=False, indent=2)[:1500])'
}

# eum sql은 SELECT·WITH로 시작하는 질의만 받으므로 앞의 -- 주석 줄은 뗀다.
sql_text() { grep -v '^[[:space:]]*--' "$1"; }

run_sql() {
  if [ "$EUM_MODE" = compose ]; then
    compose exec -T manager eum sql --data /data "$(sql_text "$1")"
  else
    "$EUM_BIN" sql --data "$EUM_DATA" "$(sql_text "$1")"
  fi
}

cmd_sql() {
  [ "$EUM_MODE" = compose ] || [ -x "$EUM_BIN" ] || go build -o "$EUM_BIN" ./cmd/eum
  say sql "열린 데이터: 질의 3개 (장면 7)"
  for q in capacity slow-functions risk-vs-incidents; do
    echo "-- $q.sql"
    run_sql "$Q/$q.sql"
    echo
  done
}

cmd_export() {
  [ $# -ge 1 ] || die "사용법: demo.sh export <exec_id>"
  id="$1"
  mkdir -p out
  say export "exec_id=$id 의 원자료를 ./out/ 에 내려받습니다 (장면 7)"
  acurl -fsS "$API/export/samples?exec_id=$id" -o "out/samples-$id.csv"
  acurl -fsS "$API/export/profile?exec_id=$id" -o "out/profile-$id.folded"
  acurl -fsS "$API/export/logs?exec_id=$id" -o "out/logs-$id.csv"
  ls -l "out/samples-$id.csv" "out/profile-$id.folded" "out/logs-$id.csv"
}

cmd_status() {
  say status "플로우별 최근 상태, 위험 지수, firing 알림"
  get flows | python3 -c '
import sys,json
for f in json.load(sys.stdin)["flows"]:
    last=f.get("last") or {}
    print("  %-18s %-12s %s" % (f["id"], f.get("board",""), last.get("status","-")))' | sort
  echo "위험 지수: $(jget risk 'd.get("index"), d.get("level"), d.get("mode")' 2>/dev/null || echo -)"
  echo "firing 알림: $(jget 'alerts?state=firing' 'len(d.get("alerts") or [])' 2>/dev/null || echo -)건"
}

[ $# -ge 1 ] || die "사용법: demo.sh <setup|up|down|warmup|run|surge|crash|quote|quote-surge|quote-rate|batch|svr-crash|settle|train|sql|export|status>"
c="$1"; shift
case "$c" in
  warmup|run|quote|batch|settle|train|export|status) need_login; "cmd_$c" "$@" ;;
  setup|up|down|surge|crash|sql) "cmd_$c" "$@" ;;
  quote-surge|quote-rate|svr-crash) "cmd_$(echo "$c" | tr - _)" "$@" ;;
  *) die "모르는 명령 '$c'" ;;
esac
