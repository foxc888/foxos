package confirmation

import (
	"errors"
	"testing"
	"time"
)

func TestTokenBindsPlanAndExpires(t *testing.T) {
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	signer, err := New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	signer.WithClock(func() time.Time { return now })
	plan := map[string]any{"method": "PATCH", "path": "/rest/example"}
	token, err := signer.Issue(plan, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Verify(token, plan); err != nil {
		t.Fatal(err)
	}
	if err := signer.Verify(token, map[string]any{"method": "DELETE"}); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("err=%v", err)
	}
	signer.WithClock(func() time.Time { return now.Add(6 * time.Minute) })
	if err := signer.Verify(token, plan); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("err=%v", err)
	}
}
func TestRejectsShortKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected key error")
	}
}
