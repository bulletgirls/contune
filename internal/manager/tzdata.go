package manager

// 시간대 파일이 없는 최소 이미지에서도 cron의 tz를 해석하도록 tzdata를 내장한다.
import _ "time/tzdata"
