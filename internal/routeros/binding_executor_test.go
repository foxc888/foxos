package routeros

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
)

type recordingWriter struct {
	calls int
}

func (w *recordingWriter) Apply(context.Context, Operation) error {
	w.calls++
	return nil
}

type acceptingVerifier struct {
	calls int
}

func (v *acceptingVerifier) Verify(context.Context, Plan) error {
	v.calls++
	return nil
}

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(context.Context, Plan) error {
	return errors.New("readback mismatch")
}

type failingCompensatingWriter struct {
	calls       int
	failAt      int
	compensated []Operation
	compensate  error
}

func (w *failingCompensatingWriter) Apply(context.Context, Operation) error {
	w.calls++
	if w.calls == w.failAt {
		return errors.New("write failed")
	}
	return nil
}

func (w *failingCompensatingWriter) Compensate(_ context.Context, operations []Operation) error {
	w.compensated = append([]Operation(nil), operations...)
	return w.compensate
}

func TestBindingExecutorRequiresUntamperedConfirmation(t *testing.T) {
	signer, err := confirmation.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	writer := &recordingWriter{}
	verifier := &acceptingVerifier{}
	executor, err := NewBindingExecutor(writer, signer)
	if err != nil {
		t.Fatal(err)
	}
	executor.WithVerifier(verifier)

	plan := Plan{
		PolicyID:             "phone",
		RequiresConfirmation: true,
		Operations: []Operation{{
			Method:       http.MethodPatch,
			Path:         "/rest/ip/dhcp-server/lease/*1",
			Body:         map[string]string{"address": "10.0.0.20", "mac-address": "AA:BB:CC:DD:EE:FF", "server": "dhcp-lan", "comment": "foxos:device:phone"},
			Summary:      "bind",
			OwnedComment: "foxos:device:phone",
		}},
	}
	token, err := signer.Issue(plan, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tampered := plan
	tampered.Operations = append([]Operation(nil), plan.Operations...)
	tampered.Operations[0].Path = "/rest/system/reboot"
	if err := executor.Execute(context.Background(), tampered, token); !errors.Is(err, confirmation.ErrPlanChanged) {
		t.Fatalf("err=%v", err)
	}
	if writer.calls != 0 {
		t.Fatalf("calls=%d", writer.calls)
	}
	if err := executor.Execute(context.Background(), plan, token); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 || verifier.calls != 1 {
		t.Fatalf("writer=%d verifier=%d", writer.calls, verifier.calls)
	}
}

func TestBindingExecutorRejectsUnownedOperation(t *testing.T) {
	signer, _ := confirmation.New([]byte("01234567890123456789012345678901"))
	writer := &recordingWriter{}
	executor, _ := NewBindingExecutor(writer, signer)
	executor.WithVerifier(&acceptingVerifier{})
	plan := Plan{
		RequiresConfirmation: true,
		Operations: []Operation{{
			Method:       http.MethodPatch,
			Path:         "/rest/ip/dhcp-server/lease/*1",
			Body:         map[string]string{"address": "10.0.0.20", "mac-address": "AA:BB:CC:DD:EE:FF", "server": "dhcp-lan", "comment": "manual"},
			OwnedComment: "manual",
		}},
	}
	token, _ := signer.Issue(plan, time.Minute)
	if err := executor.Execute(context.Background(), plan, token); !errors.Is(err, ErrUnsafeOperation) {
		t.Fatalf("err=%v", err)
	}
	if writer.calls != 0 {
		t.Fatalf("calls=%d", writer.calls)
	}
}

func TestBindingExecutorRequiresReadbackVerifier(t *testing.T) {
	signer, _ := confirmation.New([]byte("01234567890123456789012345678901"))
	writer := &recordingWriter{}
	executor, _ := NewBindingExecutor(writer, signer)
	plan := Plan{
		RequiresConfirmation: true,
		Operations: []Operation{{
			Method:       http.MethodPatch,
			Path:         "/rest/ip/dhcp-server/lease/*1",
			Body:         map[string]string{"address": "10.0.0.20", "mac-address": "AA:BB:CC:DD:EE:FF", "server": "dhcp-lan", "comment": "foxos:device:phone"},
			OwnedComment: "foxos:device:phone",
		}},
	}
	token, _ := signer.Issue(plan, time.Minute)
	if err := executor.Execute(context.Background(), plan, token); !errors.Is(err, ErrUnsafeOperation) {
		t.Fatalf("err=%v", err)
	}
	if writer.calls != 0 {
		t.Fatalf("calls=%d", writer.calls)
	}
}

func TestBindingExecutorReportsReadbackMismatch(t *testing.T) {
	signer, _ := confirmation.New([]byte("01234567890123456789012345678901"))
	writer := &recordingWriter{}
	executor, _ := NewBindingExecutor(writer, signer)
	executor.WithVerifier(rejectingVerifier{})
	plan := Plan{
		RequiresConfirmation: true,
		Operations: []Operation{{
			Method:       http.MethodPatch,
			Path:         "/rest/ip/dhcp-server/lease/*1",
			Body:         map[string]string{"address": "10.0.0.20", "mac-address": "AA:BB:CC:DD:EE:FF", "server": "dhcp-lan", "comment": "foxos:device:phone"},
			OwnedComment: "foxos:device:phone",
		}},
	}
	token, _ := signer.Issue(plan, time.Minute)
	if err := executor.Execute(context.Background(), plan, token); err == nil {
		t.Fatal("expected readback verification error")
	}
}

func TestBindingExecutorCompensatesOnlyAppliedPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		failAt          int
		verifier        PlanVerifier
		compensationErr error
		wantApplied     int
		wantError       error
	}{
		{name: "first write fails without compensation", failAt: 1, verifier: &acceptingVerifier{}, wantApplied: 0},
		{name: "second write rolls back first", failAt: 2, verifier: &acceptingVerifier{}, wantApplied: 1, wantError: ErrBindingRolledBack},
		{name: "compensation failure is explicit", failAt: 2, verifier: &acceptingVerifier{}, compensationErr: errors.New("rollback failed"), wantApplied: 1, wantError: ErrCompensationFailed},
		{name: "readback failure rolls back both writes", failAt: 99, verifier: rejectingVerifier{}, wantApplied: 2, wantError: ErrBindingRolledBack},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			signer, err := confirmation.New([]byte("01234567890123456789012345678901"))
			if err != nil {
				t.Fatal(err)
			}
			writer := &failingCompensatingWriter{failAt: test.failAt, compensate: test.compensationErr}
			executor, err := NewBindingExecutor(writer, signer)
			if err != nil {
				t.Fatal(err)
			}
			executor.WithVerifier(test.verifier)
			plan := bindingTestPlan()
			token, err := signer.Issue(plan, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			err = executor.Execute(context.Background(), plan, token)
			if err == nil {
				t.Fatal("expected execution failure")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("err=%v, want %v", err, test.wantError)
			}
			if len(writer.compensated) != test.wantApplied {
				t.Fatalf("compensated=%d, want %d", len(writer.compensated), test.wantApplied)
			}
			for index := range writer.compensated {
				if writer.compensated[index].OwnedComment != plan.Operations[index].OwnedComment {
					t.Fatalf("compensation contains unapplied operation: %+v", writer.compensated)
				}
			}
		})
	}
}

func bindingTestPlan() Plan {
	operations := make([]Operation, 0, 2)
	for index, id := range []string{"phone", "tablet"} {
		comment := "foxos:device:" + id
		operations = append(operations, Operation{
			Method:       http.MethodPut,
			Path:         "/rest/ip/dhcp-server/lease",
			Body:         map[string]string{"address": fmt.Sprintf("192.168.1.%d", 20+index), "mac-address": fmt.Sprintf("AA:BB:CC:DD:EE:F%d", index), "server": "dhcp-lan", "comment": comment},
			Summary:      "bind",
			OwnedComment: comment,
		})
	}
	return Plan{PolicyID: "batch", RequiresConfirmation: true, Operations: operations}
}
