-- 체결이 밀린 구간(p99 50ms 초과)과 평시에 체결 서비스가 CPU를 쓴 함수 비중(%)
-- 체결 서비스의 표본·지표는 서비스 주인 키 svc:matching@<서버>로 쌓인다(06 §12). 그 전 데이터는 start-matching 실행 ID.
WITH w AS (
  SELECT p.leaf, p.samples,
         EXISTS (SELECT 1 FROM v_samples m
                 WHERE m.exec_id = p.exec_id
                   AND m.name = 'biz.order.latency_p99_ms'
                   AND m.value > 50
                   AND m.ts >= p.ts AND m.ts < p.ts + p.dur_ms) AS slow
  FROM v_profiles p
  WHERE p.exec_id LIKE 'svc:matching@%'
     OR p.exec_id IN (SELECT exec_id FROM runs WHERE step_id = 'start-matching')
)
SELECT leaf,
       ROUND(100.0 * SUM(samples * (1 - slow)) / (SELECT SUM(samples * (1 - slow)) FROM w), 1) AS normal_pct,
       ROUND(100.0 * SUM(samples * slow)       / (SELECT SUM(samples * slow)       FROM w), 1) AS slow_pct
FROM w
GROUP BY leaf
ORDER BY slow_pct DESC
LIMIT 5;
