package confirmation

import (
	"errors"
	"testing"
)

func TestReplayGuardConsumesTokenOnce(t *testing.T) {
	guard := NewReplayGuard()
	if err := guard.Consume("token"); err != nil {
		t.Fatal(err)
	}
	if err := guard.Consume("token"); !errors.Is(err, ErrReplayedToken) {
		t.Fatalf("err=%v", err)
	}
	if err := guard.Consume("different"); err != nil {
		t.Fatal(err)
	}
}
