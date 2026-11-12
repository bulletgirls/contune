-- 최근 5분 업무별 건수: 채널 전달 → 체결 처리 → 시세 생성 → 분배 송출, 체결 → 통계 반영 (eum sql로 실행, demo.sh count)
-- 1초마다 오는 건/초 값을 더해 건수로 본다(표본이 빠진 초는 빠진다). 분배 송출은 시세 + 지수 전문 수다.
WITH k(ord, flow, name) AS (
  VALUES (1, '채널 전달', 'biz.channel.out_tps'),
         (2, '체결 처리', 'biz.order.tps'),
         (3, '시세 생성', 'biz.quote.sent_tps'),
         (4, '분배 송출', 'biz.dist.out_tps'),
         (5, '통계 반영', 'biz.stats.out_tps')
),
w AS (SELECT CAST(strftime('%s', 'now') AS INTEGER) * 1000 - 300000 AS since)
SELECT k.flow                                AS flow,
       CAST(ROUND(COALESCE(SUM(s.value), 0)) AS INTEGER) AS count,
       COUNT(s.value)                        AS seconds
FROM k
LEFT JOIN v_samples s
  ON s.name = k.name
 AND s.ts >= (SELECT since FROM w)
GROUP BY k.ord, k.flow
ORDER BY k.ord;
