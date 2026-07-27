package routeros

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/foxc888/foxos/internal/confirmation"
)

var ErrUnsafeOperation = errors.New("unsafe RouterOS operation")
var ErrCompensationFailed = errors.New("RouterOS compensation failed")
var ErrBindingRolledBack = errors.New("RouterOS binding changes were rolled back")

type OperationWriter interface {
	Apply(context.Context, Operation) error
}

type OperationCompensator interface {
	Compensate(context.Context, []Operation) error
}

type PlanVerifier interface {
	Verify(context.Context, Plan) error
}

type BindingExecutor struct {
	writer   OperationWriter
	signer   *confirmation.Signer
	verifier PlanVerifier
}

func NewBindingExecutor(writer OperationWriter, signer *confirmation.Signer) (*BindingExecutor, error) {
	if writer == nil || signer == nil {
		return nil, errors.New("writer and signer are required")
	}
	return &BindingExecutor{writer: writer, signer: signer}, nil
}

func (e *BindingExecutor) WithVerifier(verifier PlanVerifier) *BindingExecutor {
	e.verifier = verifier
	return e
}

func (e *BindingExecutor) Execute(ctx context.Context, plan Plan, token string) error {
	if err := e.signer.Verify(token, plan); err != nil {
		return err
	}
	if !plan.RequiresConfirmation || len(plan.Operations) == 0 || e.verifier == nil {
		return ErrUnsafeOperation
	}
	for _, operation := range plan.Operations {
		if err := validateBindingOperation(operation); err != nil {
			return err
		}
	}
	applied := make([]Operation, 0, len(plan.Operations))
	for _, operation := range plan.Operations {
		if err := e.writer.Apply(ctx, operation); err != nil {
			if len(applied) > 0 {
				if compensator, ok := e.writer.(OperationCompensator); ok {
					if compensationErr := compensator.Compensate(ctx, applied); compensationErr != nil {
						return fmt.Errorf("%w: %v (original: %v)", ErrCompensationFailed, compensationErr, err)
					}
					return fmt.Errorf("%w: %v", ErrBindingRolledBack, err)
				}
			}
			return fmt.Errorf("RouterOS operation failed: %w", err)
		}
		applied = append(applied, operation)
	}
	if err := e.verifier.Verify(ctx, plan); err != nil {
		if len(applied) > 0 {
			if compensator, ok := e.writer.(OperationCompensator); ok {
				if compensationErr := compensator.Compensate(ctx, applied); compensationErr != nil {
					return fmt.Errorf("%w: %v (original: %v)", ErrCompensationFailed, compensationErr, err)
				}
				return fmt.Errorf("%w: readback verification: %v", ErrBindingRolledBack, err)
			}
		}
		return fmt.Errorf("RouterOS readback verification failed: %w", err)
	}
	return nil
}

func validateBindingOperation(operation Operation) error {
	if operation.Method != http.MethodPut && operation.Method != http.MethodPatch {
		return fmt.Errorf("%w: method", ErrUnsafeOperation)
	}
	if operation.Path != "/rest/ip/dhcp-server/lease" &&
		!(strings.HasPrefix(operation.Path, "/rest/ip/dhcp-server/lease/") && safeRouterOSID(strings.TrimPrefix(operation.Path, "/rest/ip/dhcp-server/lease/"))) {
		return fmt.Errorf("%w: path", ErrUnsafeOperation)
	}
	if !strings.HasPrefix(operation.OwnedComment, "foxos:device:") {
		return fmt.Errorf("%w: owner", ErrUnsafeOperation)
	}
	if operation.Body["comment"] != operation.OwnedComment {
		return fmt.Errorf("%w: comment mismatch", ErrUnsafeOperation)
	}
	if isManagementAddress(operation.Body["address"]) {
		return fmt.Errorf("%w: management address", ErrUnsafeOperation)
	}
	if operation.Body["address"] == "" || operation.Body["mac-address"] == "" || operation.Body["server"] == "" {
		return fmt.Errorf("%w: incomplete binding body", ErrUnsafeOperation)
	}
	return nil
}
