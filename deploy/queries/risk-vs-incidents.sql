-- 위험 지수(risk.index) 1분 평균을 장애 구간 안·밖으로 나눠 비교
WITH r AS (
  SELECT ts / 60000 * 60000 AS minute, AVG(value) AS idx
  FROM v_samples
  WHERE name = 'risk.index'
  GROUP BY 1
),
tagged AS (
  SELECT r.minute, r.idx,
         EXISTS (SELECT 1 FROM incidents i
                 WHERE r.minute >= i.start_ts
                   AND r.minute <= CASE WHEN i.end_ts = 0 THEN 9223372036854775807 ELSE i.end_ts END) AS in_incident
  FROM r
)
SELECT CASE in_incident WHEN 1 THEN '장애 구간 안' ELSE '장애 구간 밖' END AS period,
       COUNT(*)            AS minutes,
       ROUND(AVG(idx), 1)  AS avg_index,
       ROUND(MAX(idx), 1)  AS max_index
FROM tagged
GROUP BY in_incident
ORDER BY in_incident DESC;
