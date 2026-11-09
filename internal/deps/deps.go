// Package deps는 go mod tidy가 아직 쓰지 않는 의존성을 지우지 않도록 붙잡는다.
package deps

import (
	_ "github.com/oklog/ulid/v2"
	_ "github.com/robfig/cron/v3"
	_ "gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)
