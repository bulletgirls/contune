#!/bin/sh
# 보조 플로우 37개를 flows/ 아래에 만든다(결정적: 다시 돌려도 같은 결과).
set -eu
cd "$(dirname "$0")/.."

# 배치판|id|일정|호스트|스텝 수
TABLE='개장준비|open-ref-01|0 4 * * 1-5|feed-01|2
개장준비|open-ref-02|20 4 * * 1-5|feed-02|1
개장준비|open-ref-03|40 4 * * 1-5|batch-02|3
개장준비|open-ref-04|10 5 * * 1-5|batch-03|2
개장준비|open-ref-05|50 5 * * 1-5|batch-04|2
장중|intra-feed-01|*/5 * * * *|feed-03|1
장중|intra-feed-02|*/5 * * * *|feed-04|2
장중|intra-gw-01|*/5 * * * *|gw-02|1
장중|intra-gw-02|0 10 * * 1-5|gw-04|3
장중|intra-feed-03|30 13 * * 1-5|feed-05|2
정산|settle-ref-01|30 15 * * 1-5|settle-01|2
정산|settle-ref-02|50 15 * * 1-5|settle-02|1
정산|settle-ref-03|10 16 * * 1-5|settle-03|3
정산|settle-ref-04|40 16 * * 1-5|settle-04|2
정산|settle-ref-05|10 17 * * 1-5|settle-01|2
정산|settle-ref-06|50 17 * * 1-5|settle-02|3
대외송수신|ext-send-margin|*/10 * * * *|ext-01|1
대외송수신|ext-send-trade|0 9 * * 1-5|ext-02|2
대외송수신|ext-recv-kftc|30 9 * * 1-5|ext-03|1
대외송수신|ext-recv-price|0 11 * * 1-5|ext-05|2
대외송수신|ext-send-report|30 12 * * 1-5|ext-06|3
대외송수신|ext-recv-notice|0 14 * * 1-5|ext-01|1
대외송수신|ext-send-bank|30 16 * * 1-5|ext-02|2
대외송수신|ext-recv-bank|0 17 * * 1-5|ext-03|2
대외송수신|ext-send-sec|30 18 * * 1-5|ext-05|1
정보계적재|dw-load-01|0 19 * * 1-5|dw-01|2
정보계적재|dw-load-02|20 19 * * 1-5|dw-02|3
정보계적재|dw-load-03|40 19 * * 1-5|dw-03|1
정보계적재|dw-load-04|0 20 * * 1-5|dw-04|2
정보계적재|dw-load-05|30 20 * * 1-5|dw-05|2
정보계적재|dw-load-06|0 21 * * 1-5|dw-01|3
정보계적재|dw-load-07|30 21 * * 1-5|dw-02|1
정보계적재|dw-load-08|0 23 * * 1-5|dw-03|2
주간월간|weekly-ops-01|0 2 * * 6|ops-01|2
주간월간|weekly-ops-02|30 2 * * 6|ops-02|1
주간월간|monthly-ops-01|0 3 1 * *|ops-03|3
주간월간|monthly-ops-02|30 3 1 * *|ops-04|2'

# 만들어 두었던 보조 플로우만 지우고 다시 만든다(시연 플로우 market-open·daily-settle은 건드리지 않음).
echo "$TABLE" | while IFS='|' read -r board id sched host n; do
  mkdir -p "flows/$board"
  f="flows/$board/$id.yaml"
  {
    echo "# 보조 플로우($board): 모의 서버에서 CPU를 몇 초 쓰고 끝난다. deploy/gen_flows.sh로 만든다."
    echo "id: $id"
    echo "schedule: \"$sched\""
    echo "steps:"
    i=1
    while [ "$i" -le "$n" ]; do
      d=$(( (${#id} * 3 + i * 2 + ${#sched}) % 7 + 2 ))
      echo "  - id: step-$i"
      echo "    host: $host"
      if [ "$id" = ext-send-margin ]; then
        echo "    command: [sh, -c, 'echo \"(E) 증거금 파일 전송 실패\"; exit 2']"
        echo "    retry: { count: 2, interval: 10s }"
      else
        echo "    command: [eum, burn, --cpu, \"1\", --duration, ${d}s]"
      fi
      [ "$i" -gt 1 ] && echo "    depends: [step-1]" || true
      i=$((i + 1))
    done
  } > "$f"
done
