package gitwatch

import (
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

var _ Repository = (*store.Store)(nil)

func TestStoreImplementsRepository(t *testing.T) {}
