//go:build !linux

package kmsg

import (
	"errors"
	"io"
	"time"
)

func openKmsg() (io.Reader, error) { return nil, errors.New("커널 로그는 Linux만") }

func isEPIPE(error) bool { return false }

func bootWall() time.Time { return time.Time{} }
