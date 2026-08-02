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

type PlanPreconditionVerifier interface {
	CheckPrecondition(context.Context, Plan) error
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
	if !plan.RequiresConfirmation || len(plan.Operations) == 0 || len(plan.Operations) > maxBindingOperations || e.verifier == nil || plan.PreState.Digest == "" {
		return ErrUnsafeOperation
	}
	for _, operation := range plan.Operations {
		if err := validateBindingOperation(operation, plan.ProtectedAddresses); err != nil {
			return err
		}
	}
	preconditions, ok := e.verifier.(PlanPreconditionVerifier)
	if !ok {
		return ErrUnsafeOperation
	}
	if err := preconditions.CheckPrecondition(ctx, plan); err != nil {
		return err
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

func validateBindingOperation(operation Operation, protected ...[]string) error {
	protectedAddresses := normalizedProtectedAddresses(nil)
	if len(protected) > 0 {
		protectedAddresses = normalizedProtectedAddresses(protected[0])
	}
	if !strings.HasPrefix(operation.OwnedComment, "foxos:device:") || operation.After == nil {
		return fmt.Errorf("%w: owner or expected state", ErrUnsafeOperation)
	}
	if operation.Method == http.MethodPost {
		if operation.Path != "/rest/ip/dhcp-server/lease/make-static" || operation.Before == nil || operation.Before.Dynamic != "true" || operation.After.Dynamic == "true" || operation.Body[".id"] != operation.Before.ID || operation.After.ID != operation.Before.ID || !safeRouterOSID(operation.Before.ID) {
			return fmt.Errorf("%w: make-static operation", ErrUnsafeOperation)
		}
		return validateBindingRollback(operation)
	}
	if operation.Method != http.MethodPut && operation.Method != http.MethodPatch {
		return fmt.Errorf("%w: method", ErrUnsafeOperation)
	}
	if operation.Path != "/rest/ip/dhcp-server/lease" &&
		!(strings.HasPrefix(operation.Path, "/rest/ip/dhcp-server/lease/") && safeRouterOSID(strings.TrimPrefix(operation.Path, "/rest/ip/dhcp-server/lease/"))) {
		return fmt.Errorf("%w: path", ErrUnsafeOperation)
	}
	if operation.Body["comment"] != operation.OwnedComment {
		return fmt.Errorf("%w: comment mismatch", ErrUnsafeOperation)
	}
	if isProtectedAddress(operation.Body["address"], protectedAddresses) {
		return fmt.Errorf("%w: management address", ErrUnsafeOperation)
	}
	if operation.Body["address"] == "" || operation.Body["mac-address"] == "" || operation.Body["server"] == "" {
		return fmt.Errorf("%w: incomplete binding body", ErrUnsafeOperation)
	}
	if operation.Method == http.MethodPatch && operation.Before == nil {
		return fmt.Errorf("%w: missing patch pre-state", ErrUnsafeOperation)
	}
	if !leaseBodyMatchesState(operation.Body, *operation.After) {
		return fmt.Errorf("%w: expected state mismatch", ErrUnsafeOperation)
	}
	return validateBindingRollback(operation)
}

func validateBindingRollback(operation Operation) error {
	if operation.Rollback == nil {
		if operation.Method != http.MethodPut {
			return fmt.Errorf("%w: missing rollback", ErrUnsafeOperation)
		}
		return nil
	}
	rollback := operation.Rollback
	if rollback.Method != http.MethodPatch && rollback.Method != http.MethodDelete {
		return fmt.Errorf("%w: rollback method", ErrUnsafeOperation)
	}
	if !strings.HasPrefix(rollback.Path, "/rest/ip/dhcp-server/lease/") || !safeRouterOSID(strings.TrimPrefix(rollback.Path, "/rest/ip/dhcp-server/lease/")) {
		return fmt.Errorf("%w: rollback path", ErrUnsafeOperation)
	}
	if operation.Before == nil {
		return fmt.Errorf("%w: rollback without pre-state", ErrUnsafeOperation)
	}
	if rollback.Method == http.MethodPatch && !leaseBodyMatchesState(rollback.Body, *operation.Before) {
		return fmt.Errorf("%w: rollback body", ErrUnsafeOperation)
	}
	if rollback.Method == http.MethodDelete && operation.Method != http.MethodPost {
		return fmt.Errorf("%w: delete rollback", ErrUnsafeOperation)
	}
	return nil
}

func leaseBodyMatchesState(body map[string]string, state LeaseState) bool {
	return body["address"] == state.Address && normalizeMAC(body["mac-address"]) == state.MACAddress && body["server"] == state.Server && body["comment"] == state.Comment && normalizedBool(body["disabled"]) == state.Disabled
}

func normalizedBool(value string) string {
	if value == "" {
		return "false"
	}
	return value
}
