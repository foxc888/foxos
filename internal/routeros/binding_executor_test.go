package routeros

import (
	"context"
	"errors"
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
			Body:         map[string]string{"comment": "foxos:device:phone"},
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
			Body:         map[string]string{"comment": "manual"},
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
			Body:         map[string]string{"comment": "foxos:device:phone"},
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
			Body:         map[string]string{"comment": "foxos:device:phone"},
			OwnedComment: "foxos:device:phone",
		}},
	}
	token, _ := signer.Issue(plan, time.Minute)
	if err := executor.Execute(context.Background(), plan, token); err == nil {
		t.Fatal("expected readback verification error")
	}
}
