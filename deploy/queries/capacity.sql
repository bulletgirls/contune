-- 접수량 구간별 체결 p99: 초당 몇 건부터 밀리기 시작하나 (eum sql로 실행)
-- 잰 접수율은 목표값 주변에서 몇 건씩 흔들린다(6,000건/초에서 5,992~6,006). 구간 경계에 75건(구간 폭의 5%) 안쪽으로
-- 모자란 값은 그 구간으로 센다. 그냥 버리면 6,000건/초로 밀린 초의 절반이 4,500 구간에 섞인다.
SELECT CAST((i.value + 75) / 1500 AS INT) * 1500 AS in_tps,
       COUNT(*)               AS seconds,
       ROUND(AVG(p.value), 1) AS avg_p99_ms,
       ROUND(MAX(p.value), 1) AS max_p99_ms
FROM v_samples i
JOIN v_samples p
  ON p.exec_id = i.exec_id
 AND p.ts = i.ts
 AND p.name = 'biz.order.latency_p99_ms'
WHERE i.host = 'match-01'
  AND i.name = 'biz.order.in_tps'
GROUP BY 1
ORDER BY 1;
