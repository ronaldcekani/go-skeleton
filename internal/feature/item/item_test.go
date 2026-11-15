package item

import (
	"strings"
	"testing"

	"github.com/example/go-skeleton/internal/domain"
)

func TestCreateInputValidate(t *testing.T) {
	for name, tc := range map[string]struct {
		in    CreateInput
		valid bool
	}{
		"ok":                   {CreateInput{Name: "widget", Description: "a thing"}, true},
		"max lengths":          {CreateInput{Name: strings.Repeat("n", MaxNameLen), Description: strings.Repeat("d", MaxDescriptionLen)}, true},
		"max length multibyte": {CreateInput{Name: strings.Repeat("й", MaxNameLen)}, true}, // characters, not bytes
		"missing name":         {CreateInput{}, false},
		"name too long":        {CreateInput{Name: strings.Repeat("n", MaxNameLen+1)}, false},
		"description too long": {CreateInput{Name: "widget", Description: strings.Repeat("d", MaxDescriptionLen+1)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.valid && err != nil {
				t.Errorf("Validate = %v, want nil", err)
			}
			if !tc.valid && !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("Validate = %v, want invalid input", err)
			}
		})
	}
}

func TestListInputValidate(t *testing.T) {
	for _, in := range []ListInput{{}, {Limit: 1}, {Limit: MaxPageSize, Offset: 500}} {
		if err := in.Validate(); err != nil {
			t.Errorf("%+v: Validate = %v, want nil", in, err)
		}
	}
	for _, in := range []ListInput{{Limit: -1}, {Limit: MaxPageSize + 1}, {Limit: 10, Offset: -1}} {
		if err := in.Validate(); !domain.IsKind(err, domain.ErrKindInvalidInput) {
			t.Errorf("%+v: Validate = %v, want invalid input", in, err)
		}
	}
}
