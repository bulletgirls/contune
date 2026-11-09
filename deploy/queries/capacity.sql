-- 접수량 구간별 체결 p99: 초당 몇 건부터 밀리기 시작하나 (eum sql로 실행)
SELECT CAST(i.value / 1500 AS INT) * 1500 AS in_tps,
       COUNT(*)               AS seconds,
       ROUND(AVG(p.value), 1) AS avg_p99_ms,
       MAX(p.value)           AS max_p99_ms
FROM v_samples i
JOIN v_samples p
  ON p.exec_id = i.exec_id
 AND p.ts = i.ts
 AND p.name = 'biz.order.latency_p99_ms'
WHERE i.host = 'match-01'
  AND i.name = 'biz.order.in_tps'
GROUP BY 1
ORDER BY 1;
