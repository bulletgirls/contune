// Package fleet은 설정 파일에 적은 모의 서버 N대를 한 프로세스의 에이전트로 띄운다(02 §13).
package fleet

import (
	"context"
	"errors"
	"time"
)

// Config는 fleet.yaml이다. (stub: F1이 Hosts·Faults 등을 채움)
type Config struct {
	Manager  string        `yaml:"manager"`
	Token    string        `yaml:"token"`
	Interval time.Duration `yaml:"interval"`
}

// LoadConfig는 fleet.yaml을 읽는다. (stub)
func LoadConfig(path string) (Config, error) {
	return Config{}, errors.New("not implemented")
}

// Run은 모의 서버마다 에이전트를 띄워 ctx가 끝날 때까지 돈다. (stub)
func Run(ctx context.Context, cfg Config) error {
	return errors.New("not implemented")
}
