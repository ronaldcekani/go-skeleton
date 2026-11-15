package domain

import (
	"errors"
	"fmt"
	"testing"
)

func TestKindOf(t *testing.T) {
	cause := errors.New("connection refused")
	for name, tc := range map[string]struct {
		err     error
		kind    ErrKind
		message string
	}{
		"plain error is internal":    {cause, ErrKindInternal, "internal server error"},
		"not found":                  {NotFound("item not found"), ErrKindNotFound, "item not found"},
		"wrapped by fmt":             {fmt.Errorf("get item: %w", NotFound("item not found")), ErrKindNotFound, "item not found"},
		"wrapped cause stays hidden": {WrapError(ErrKindConflict, cause, "name already taken"), ErrKindConflict, "name already taken"},
		"internal hides its message": {WrapError(ErrKindInternal, cause, "query failed"), ErrKindInternal, "internal server error"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := KindOf(tc.err); got != tc.kind {
				t.Errorf("KindOf = %v, want %v", got, tc.kind)
			}
			if got := PublicMessage(tc.err); got != tc.message {
				t.Errorf("PublicMessage = %q, want %q", got, tc.message)
			}
		})
	}

	wrapped := WrapError(ErrKindConflict, cause, "name already taken")
	if !errors.Is(wrapped, cause) {
		t.Error("WrapError must keep the cause in the chain")
	}
	if IsKind(nil, ErrKindInternal) {
		t.Error("IsKind(nil, ...) must be false")
	}
}
