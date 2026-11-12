-- 배포 전후 비교: start-matching의 최근 두 실행에서 함수(leaf)별 CPU 비중(%)을 나란히 (기획서 12쪽)
-- rn=1이 가장 최근(배포 후), rn=2가 그 직전(배포 전)
-- 함수 표본은 서비스 주인 키(svc:matching@<서버>)로 쌓여 여러 실행의 것이 한 키에 섞인다(06, 11-11).
-- 그래서 실행 하나의 몫 = 그 실행이 시작한 때부터 같은 서버의 다음 start-matching이 시작하기 전까지의 표본.
-- 11-11 전 데이터는 실행 ID로 쌓였으므로 그것도 본다.
WITH runs_m AS (
  SELECT exec_id, host,
         COALESCE(started_at, scheduled_at) AS t0,
         LAG(COALESCE(started_at, scheduled_at))
           OVER (PARTITION BY host ORDER BY COALESCE(started_at, scheduled_at) DESC) AS t1,
         ROW_NUMBER() OVER (ORDER BY COALESCE(started_at, scheduled_at) DESC) AS rn
  FROM runs
  WHERE step_id = 'start-matching'
),
pick AS (
  SELECT exec_id, host, t0, t1, rn FROM runs_m WHERE rn <= 2
),
mine AS (
  SELECT pick.rn, p.leaf, p.samples
  FROM v_profiles p
  JOIN pick
    ON p.exec_id = pick.exec_id
    OR (p.exec_id = 'svc:matching@' || pick.host
        AND p.ts >= pick.t0
        AND (pick.t1 IS NULL OR p.ts < pick.t1))
),
tot AS (
  SELECT rn, SUM(samples) AS total FROM mine GROUP BY rn
),
share AS (
  SELECT m.leaf, m.rn, 100.0 * SUM(m.samples) / tot.total AS pct
  FROM mine m
  JOIN tot ON tot.rn = m.rn
  GROUP BY m.leaf, m.rn
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
