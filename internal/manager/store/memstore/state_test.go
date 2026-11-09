package memstore_test

import (
	"testing"

	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/manager/store/storetest"
)

func TestState(t *testing.T) {
	storetest.RunState(t, func(t *testing.T) store.StateStore { return memstore.NewState() })
}
