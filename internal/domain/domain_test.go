package domain

import (
	"errors"
	"fmt"
	"strings"
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

func TestCreateItemInputValidate(t *testing.T) {
	for name, tc := range map[string]struct {
		in    CreateItemInput
		valid bool
	}{
		"ok":                   {CreateItemInput{Name: "widget", Description: "a thing"}, true},
		"max lengths":          {CreateItemInput{Name: strings.Repeat("n", MaxItemNameLen), Description: strings.Repeat("d", MaxItemDescriptionLen)}, true},
		"missing name":         {CreateItemInput{}, false},
		"name too long":        {CreateItemInput{Name: strings.Repeat("n", MaxItemNameLen+1)}, false},
		"description too long": {CreateItemInput{Name: "widget", Description: strings.Repeat("d", MaxItemDescriptionLen+1)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.valid && err != nil {
				t.Errorf("Validate = %v, want nil", err)
			}
			if !tc.valid && !IsKind(err, ErrKindInvalidInput) {
				t.Errorf("Validate = %v, want invalid input", err)
			}
		})
	}
}

func TestListItemsInputValidate(t *testing.T) {
	for _, in := range []ListItemsInput{{}, {Limit: 1}, {Limit: MaxPageSize, Offset: 500}} {
		if err := in.Validate(); err != nil {
			t.Errorf("%+v: Validate = %v, want nil", in, err)
		}
	}
	for _, in := range []ListItemsInput{{Limit: -1}, {Limit: MaxPageSize + 1}, {Limit: 10, Offset: -1}} {
		if err := in.Validate(); !IsKind(err, ErrKindInvalidInput) {
			t.Errorf("%+v: Validate = %v, want invalid input", in, err)
		}
	}
}
