-- 배포 전후 비교: start-matching의 최근 두 실행에서 함수(leaf)별 CPU 비중(%)을 나란히 (기획서 12쪽)
-- rn=1이 가장 최근(배포 후), rn=2가 그 직전(배포 전)
WITH last2 AS (
  SELECT exec_id,
         ROW_NUMBER() OVER (ORDER BY COALESCE(started_at, scheduled_at) DESC) AS rn
  FROM runs
  WHERE step_id = 'start-matching'
),
pick AS (
  SELECT exec_id, rn FROM last2 WHERE rn <= 2
),
tot AS (
  SELECT p.exec_id, SUM(p.samples) AS total
  FROM v_profiles p JOIN pick ON pick.exec_id = p.exec_id
  GROUP BY p.exec_id
),
share AS (
  SELECT p.leaf, pick.rn, 100.0 * SUM(p.samples) / tot.total AS pct
  FROM v_profiles p
  JOIN pick ON pick.exec_id = p.exec_id
  JOIN tot  ON tot.exec_id  = p.exec_id
  GROUP BY p.leaf, pick.rn
)
SELECT leaf,
       ROUND(SUM(CASE WHEN rn = 2 THEN pct END), 1) AS before_pct,
       ROUND(SUM(CASE WHEN rn = 1 THEN pct END), 1) AS after_pct,
       ROUND(COALESCE(SUM(CASE WHEN rn = 1 THEN pct END), 0)
           - COALESCE(SUM(CASE WHEN rn = 2 THEN pct END), 0), 1) AS diff_pct
FROM share
GROUP BY leaf
ORDER BY ABS(diff_pct) DESC, leaf
LIMIT 10;
