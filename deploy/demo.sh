#!/usr/bin/env bash
# 시연 진행 스크립트: deploy/demo.sh <setup|up|down|warmup|run|surge|crash|quote|load|index-crash|channel-slow|channel-ok|trace|count|quote-surge|quote-rate|batch|svr-crash|settle|train|sql|export|status>
# 업무 프로세스 여섯이 저널로 주문을 넘긴다(카드 V): 채널 gw-01 → 체결 match-01 → 시세 feed-01 → 지수산출 idx-01 → 분배 feed-02,
#   체결 → 매매통계 dw-01. run(market-open: 통계 → 체결 → 채널), quote(quote-open: 분배 → 지수 → 시세)로 받는 쪽부터 연다.
# 장면(카드 V §8): surge(주문 폭주, 채널 접수율) · load(통계 적체, 정보계 적재) · index-crash(지수산출 죽음, 되돌리기는 quote) ·
#   channel-slow·channel-ok(채널 느려짐) · crash(체결 죽음, 되돌리기는 run) · trace [seq](주문 하나의 경로) · count(최근 5분 건수)
# 환경변수: EUM_URL(기본 http://localhost:8080) EUM_MODE(dev|compose, 기본 dev) EUM_DATA(기본 ./data-dev)
#          EUM_COMPOSE(compose 파일, 기본 deploy/docker-compose.yml) EUM_DEV_FLAGS(eum dev에 더 넘길 플래그)
#          EUM_HOSTS(up이 기다리는 호스트 수, 기본 46) EUM_BIN(eum 실행 파일, 기본 deploy/bin/eum) EUM_TOKEN(기본 dev)
#          EUM_USER·EUM_PASS(화면 로그인 계정, 기본 admin/eum-admin. 로그인이 꺼져 있으면 쓰지 않음)
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# LLM 설정(카드 N·Q): deploy/.env.local(커밋하지 않는 파일)이 있으면 읽어 dev 매니저와 compose에 넘긴다.
#   EUM_LLM=anthropic + ANTHROPIC_API_KEY=…  |  EUM_LLM=claude-cli(+ compose면 CLAUDE_CODE_OAUTH_TOKEN=…, EUM_CLAUDE_CLI=1)
#   EUM_CODE_LLM=off|snippets|tools(dev, 비면 tools): 종료 보고서 분석 때 LLM에 코드를 보내는 범위
if [ -f deploy/.env.local ]; then set -a; . deploy/.env.local; set +a; fi

EUM_URL="${EUM_URL:-http://localhost:8080}"
EUM_MODE="${EUM_MODE:-dev}"
EUM_DATA="${EUM_DATA:-./data-dev}"
EUM_COMPOSE="${EUM_COMPOSE:-deploy/docker-compose.yml}"
EUM_DEV_FLAGS="${EUM_DEV_FLAGS:-}"
EUM_HOSTS="${EUM_HOSTS:-46}"   # up이 기다리는 호스트 수
EUM_BIN="${EUM_BIN:-deploy/bin/eum}"
export EUM_TOKEN="${EUM_TOKEN:-dev}"
export EUM_USER="${EUM_USER:-admin}"
export EUM_PASS="${EUM_PASS:-eum-admin}"
API="$EUM_URL/api/v1"
RUN="$EUM_DATA"
COOKIES="$RUN/demo.cookies"   # 화면 로그인 세션(API를 부르는 curl이 모두 같이 쓴다)
Q=deploy/queries

# 보조 플로우 12개(ext-send-margin은 일부러 실패해 "실패 1"을 만든다). dw-load-01은 정보계 적재 장면(load)이라 빼고 dw-load-03
WARMUP_FLOWS="ext-send-margin ext-send-bank ext-send-sec ext-recv-bank ext-recv-kftc intra-feed-01 intra-gw-01 dw-load-03 dw-load-02 open-ref-01 settle-ref-01 weekly-ops-01"

say() { echo "[demo:$1] $2"; }
die() { echo "demo.sh: $*" >&2; exit 1; }
# 컨테이너는 이 PC 사용자 uid로 돈다(바인드 마운트한 flows 등에 쓸 수 있게, docker-compose.yml의 user)
# (id -g는 sg docker로 돌리면 docker 그룹이 나오므로 저장소 폴더의 주인을 쓴다)
compose() { EUM_UID="${EUM_UID:-$(stat -c %u "$ROOT")}" EUM_GID="${EUM_GID:-$(stat -c %g "$ROOT")}" docker compose -f "$EUM_COMPOSE" "$@"; }
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

# write_rate <호스트> <낱말>: 업무 프로세스가 1초마다 다시 읽는 작업 폴더의 rate 파일에 쓴다.
# 정수 = 접수율(건/초, 채널만 뜻이 있다) · crash = 죽는 결함 · slow = 느려짐 · 빈 값 = 결함 없음(demo/flow.h fl_rate).
write_rate() {
  if [ "$EUM_MODE" = compose ]; then
    compose exec -T "$1" sh -c "echo '$2' > /var/lib/eum/work/rate"
  else
    mkdir -p "$EUM_DATA/work/$1"
    echo "$2" > "$EUM_DATA/work/$1/rate"
  fi
}
set_rate() { write_rate gw-01 "$1"; }       # 주문 접수율(채널 서비스)
quote_rate() { write_rate feed-01 "$1"; }   # 시세 서비스 수신율(저널 없이 단독으로 띄웠을 때만 뜻이 있다)

host_count() { get hosts 2>/dev/null | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["hosts"]))' 2>/dev/null || echo 0; }

cmd_setup() {
  if [ "$EUM_MODE" = compose ]; then
    say setup "compose 모드는 준비할 것이 없습니다(up이 이미지를 빌드)"
    return
  fi
  say setup "업무 프로세스 여섯(demo/*_svc.c) 빌드, eum 빌드, 7일치 이력 시드 (장면 0)"
  sh demo/build.sh
  mkdir -p "$(dirname "$EUM_BIN")"
  go build -o "$EUM_BIN" ./cmd/eum
  "$EUM_BIN" seed --data "$EUM_DATA" --days 7 || say setup "seed 실패(E5 전이면 정상): 계속합니다"
}

cmd_up() {
  say up "시연 환경을 띄우고 /hosts가 ${EUM_HOSTS}대가 될 때까지 기다립니다 (장면 2)"
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
  for p in channel_svc matching_svc quote_svc index_svc dist_svc stats_svc; do
    pkill -f "$ROOT/deploy/bin/$p" 2>/dev/null || true
  done
  # 정보계 적재(dw_load)는 스텝 프로세스 그룹이라 eum dev와 같이 끝나지 않을 수 있다
  if data_abs=$(cd "$EUM_DATA" 2>/dev/null && pwd); then
    pkill -f "$data_abs/work/dw-01/dw_load" 2>/dev/null || true
  fi
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
  say run "load-equity 재료를 지우고 market-open을 실행합니다(매매통계 → 체결 → 채널, 장면 3)"
  if [ "$EUM_MODE" = compose ]; then
    compose exec -T batch-01 rm -f /var/lib/eum/work/.load.once
  else
    rm -f "$EUM_DATA/work/batch-01/.load.once"
  fi
  set_rate 1500           # 채널 접수율 1500, slow 스위치가 남았으면 걷는다
  write_rate match-01 ""  # 체결 crash 스위치가 남았으면 걷어 정상으로 띄운다
  code=$(post "flows/market-open/run")
  echo "  market-open -> HTTP $code"
  [ "$code" = 201 ] || die "market-open 실행 실패(HTTP $code)"
}

# 6000은 60초: 위험도는 최근 60초 특징으로 매기므로 6000이 20초뿐이면 0 그대로다(11-11 리허설: 약 50초 유지해야 오름).
cmd_surge() {
  say surge "접수율 3000(20초) -> 4500(20초) -> 6000(60초) -> 1500 (장면 5)"
  for step in 3000:20 4500:20 6000:60 1500:0; do
    r=${step%:*}
    set_rate "$r"
    echo "  $(date +%H:%M:%S) 접수율 $r"
    sleep "${step#*:}"
  done
}

cmd_crash() {
  say crash "체결 서비스 버그 스위치를 켭니다(접수율 파일에 crash)"
  write_rate match-01 crash
  echo "  몇 초 안에 체결 서비스가 죽고 종료 보고서가 생깁니다(#exit). 다시 띄우려면 demo.sh run"
}

# 시세 흐름: 체결 저널을 읽는 시세(feed-01 quote_svc) → 지수산출(idx-01 index_svc) → 분배(feed-02 dist_svc).
cmd_quote() {
  say quote "quote-open을 실행합니다(분배 → 지수산출 → 시세)"
  quote_rate 2000          # 저널 없이 단독으로 띄웠을 때의 수신율
  write_rate idx-01 ""     # index-crash 스위치가 남았으면 걷는다
  code=$(post "flows/quote-open/run")
  echo "  quote-open -> HTTP $code"
  [ "$code" = 201 ] || die "quote-open 실행 실패(HTTP $code)"
}

# quote-surge·quote-rate: 시세 서비스를 저널 없이 단독으로 띄웠을 때(스스로 입력을 만든다)만 뜻이 있다.
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

# 장면 B 통계 적체: dw-01에서 정보계 적재(dw_load 8개, 90초)가 매매통계와 CPU를 나눠 쓴다.
# 화면에서 dw-load-01 step-1을 강제 종료하면 적재 프로세스가 같이 내려가고 처리 적체가 0으로 돌아온다.
cmd_load() {
  say load "dw-load-01을 실행해 dw-01에 정보계 적재를 띄웁니다(매매통계 처리 적체 장면)"
  code=$(post "flows/dw-load-01/run")
  echo "  dw-load-01 -> HTTP $code"
  [ "$code" = 201 ] || die "dw-load-01 실행 실패(HTTP $code)"
}

# 장면 C 지수산출 죽음: 지수 계산(recalc_index)이 0으로 나눠 SIGFPE로 죽는다. 다시 띄우려면 demo.sh quote.
cmd_index_crash() {
  say index-crash "지수산출 서비스 결함 스위치를 켭니다(idx-01 rate에 crash)"
  write_rate idx-01 crash
  echo "  몇 초 안에 지수산출 서비스가 죽고 종료 보고서가 생깁니다(#exit). 다시 띄우려면 demo.sh quote"
}

# 예비 장면: 채널 세션 확인이 느려져 TAT만 오른다(체결 RT는 그대로).
cmd_channel_slow() {
  say channel-slow "채널 느려짐 스위치를 켭니다(gw-01 rate에 slow, 접수율은 그대로)"
  write_rate gw-01 slow
  echo "  되돌리려면 demo.sh channel-ok"
}

cmd_channel_ok() {
  say channel-ok "채널 느려짐 스위치를 끕니다(접수율 1500)"
  set_rate 1500
}

# trace [seq]: 주문 하나가 지난 업무를 시간순으로 보인다(업무 프로세스의 TRACE 줄, seq가 10,000의 배수인 주문만).
# 번호가 없으면 가장 최근 TRACE의 번호를 쓴다. 로그 검색(GET /logs?q=)은 최근 1시간.
cmd_trace() {
  seq="${1:-}"
  if [ -z "$seq" ]; then
    seq=$(get "logs?q=TRACE+seq%3D&level=info&limit=1" | python3 -c '
import sys,json,re
ev=json.load(sys.stdin)["events"]
m=re.search(r"seq=(\d+)", ev[0]["message"]) if ev else None
print(m.group(1) if m else "")') || seq=""
    [ -n "$seq" ] || die "최근 1시간에 TRACE 줄이 없습니다(run·quote 뒤 몇 초 기다리세요)"
  fi
  case "$seq" in *[!0-9]*) die "trace의 번호는 정수: '$seq'" ;; esac
  say trace "주문 $seq 의 경로"
  get "logs?q=seq%3D$seq+hop%3D&level=info&limit=50" | python3 -c '
import sys,json,re,time
# 로그 시각은 초 단위라 순서가 섞인다: 줄에 적힌 "접수부터 걸린 시간"으로 줄 세운다
def e2e_of(e):
    m=re.search(r"e2e_ms=([0-9.]+)", e["message"])
    return float(m.group(1)) if m else 1e18
ev=sorted(json.load(sys.stdin)["events"], key=e2e_of)
if not ev:
    print("  (찾지 못함: 최근 1시간의 TRACE 줄에 이 번호가 없습니다)")
for e in ev:
    hop=re.search(r"hop=(\S+)", e["message"]); e2e=re.search(r"e2e_ms=(\S+)", e["message"])
    t=time.strftime("%H:%M:%S", time.localtime(e["ts"]/1000)) + ".%03d" % (e["ts"] % 1000)
    print("  %s  %-9s %-8s 접수부터 %s ms" % (t, e["host"], hop.group(1) if hop else "-", e2e.group(1) if e2e else "-"))'
}

# count: 최근 5분 업무별 건수(채널 전달 · 체결 처리 · 시세 생성 · 분배 송출 · 통계 반영)를 한 줄씩.
cmd_count() {
  [ "$EUM_MODE" = compose ] || [ -x "$EUM_BIN" ] || go build -o "$EUM_BIN" ./cmd/eum
  say count "최근 5분 업무별 건수"
  run_sql "$Q/flow-count.sql"
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

# export <주인>: 실행 ID 또는 서비스 주인 키. 체결·시세 서비스의 지표·프로파일·로그는 서비스 주인 키로 쌓인다(06 §12):
#   deploy/demo.sh export svc:matching@match-01
cmd_export() {
  [ $# -ge 1 ] || die "사용법: demo.sh export <exec_id | svc:<서비스>@<서버>>  (체결은 svc:matching@match-01)"
  id="$1"
  enc=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$id")
  name=$(printf '%s' "$id" | tr ':@*/' '____')   # 파일 이름에 쓸 수 없는 글자(Windows의 :) 빼기
  mkdir -p out
  say export "$id 의 원자료를 ./out/ 에 내려받습니다 (장면 7)"
  acurl -fsS "$API/export/samples?exec_id=$enc" -o "out/samples-$name.csv"
  acurl -fsS "$API/export/profile?exec_id=$enc" -o "out/profile-$name.folded"
  acurl -fsS "$API/export/logs?exec_id=$enc" -o "out/logs-$name.csv"
  ls -l "out/samples-$name.csv" "out/profile-$name.folded" "out/logs-$name.csv"
  [ -s "out/profile-$name.folded" ] || case "$id" in
    svc:*) ;;
    *) echo "  프로파일이 비었습니다. 서비스로 옮겨 간 프로세스면 서비스 주인 키로: demo.sh export svc:<서비스>@<서버>" ;;
  esac
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

[ $# -ge 1 ] || die "사용법: demo.sh <setup|up|down|warmup|run|surge|crash|quote|load|index-crash|channel-slow|channel-ok|trace|count|quote-surge|quote-rate|batch|svr-crash|settle|train|sql|export|status>"
c="$1"; shift
case "$c" in
  warmup|run|quote|load|trace|batch|settle|train|export|status) need_login; "cmd_$c" "$@" ;;
  setup|up|down|surge|crash|count|sql) "cmd_$c" "$@" ;;
  index-crash|channel-slow|channel-ok|quote-surge|quote-rate|svr-crash) "cmd_$(echo "$c" | tr - _)" "$@" ;;
  *) die "모르는 명령 '$c'" ;;
esac
