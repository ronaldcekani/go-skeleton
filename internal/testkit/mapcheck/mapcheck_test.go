package mapcheck

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

type source struct {
	ID      [16]byte
	Name    string
	Count   int
	Active  bool
	Created time.Time
	Rate    *big.Rat
	Parent  *[16]byte
	Lines   []sourceLine
}

type sourceLine struct {
	SKU string
	Qty int
}

type result struct {
	ID      string
	Name    string
	Count   int64
	Active  bool
	Created time.Time
	Rate    string
	Parent  string
	Lines   []resultLine
}

type resultLine struct {
	SKU string
	Qty int
}

func complete(s source) result {
	r := result{
		ID:      fmt.Sprintf("%x", s.ID),
		Name:    s.Name,
		Count:   int64(s.Count),
		Active:  s.Active,
		Created: s.Created,
		Rate:    s.Rate.String(),
		Parent:  fmt.Sprintf("%x", *s.Parent),
	}
	for _, l := range s.Lines {
		r.Lines = append(r.Lines, resultLine(l))
	}
	return r
}

// recorder stands in for *testing.T and keeps the failures.
type recorder struct{ errors []string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(f string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(f, args...))
}
func (r *recorder) Fatalf(f string, args ...any) { panic(fmt.Sprintf(f, args...)) }

func TestCheckAcceptsACompleteMapper(t *testing.T) {
	Check(t, complete)
}

func TestCheckFindsWhatAMapperDrops(t *testing.T) {
	for name, tc := range map[string]struct {
		mapper func(source) result
		want   []string
	}{
		"field not copied": {
			func(s source) result { r := complete(s); r.Name = ""; return r },
			[]string{"result.Name is empty", "source.Name is ignored"},
		},
		"source field not read": {
			func(s source) result { r := complete(s); r.Count = 7; return r },
			[]string{"source.Count is ignored"},
		},
		"bool not copied": {
			func(s source) result { r := complete(s); r.Active = true; return r },
			[]string{"source.Active is ignored"},
		},
		"time not copied": {
			func(s source) result { r := complete(s); r.Created = time.Time{}; return r },
			[]string{"result.Created is empty", "source.Created is ignored"},
		},
		"pointer not read": {
			func(s source) result { r := complete(s); r.Parent = "x"; return r },
			[]string{"source.Parent is ignored"},
		},
		"nested field not copied": {
			func(s source) result { r := complete(s); r.Lines[0].Qty = 0; return r },
			[]string{"result.Lines.Qty is empty", "source.Lines.Qty is ignored"},
		},
		"slice dropped": {
			func(s source) result { r := complete(s); r.Lines = nil; return r },
			[]string{"result.Lines is empty", "source.Lines.SKU is ignored", "source.Lines.Qty is ignored"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			Check(rec, tc.mapper)
			if len(rec.errors) != len(tc.want) {
				t.Fatalf("got %d failures, want %d:\n%s", len(rec.errors), len(tc.want), strings.Join(rec.errors, "\n"))
			}
			for _, want := range tc.want {
				if !strings.Contains(strings.Join(rec.errors, "\n"), want) {
					t.Errorf("no failure mentions %q:\n%s", want, strings.Join(rec.errors, "\n"))
				}
			}
		})
	}
}

func TestIgnoredFieldsAreNotReported(t *testing.T) {
	dropsName := func(s source) result { r := complete(s); r.Name = ""; return r }

	rec := &recorder{}
	Check(rec, dropsName, IgnoreSource("Name"), IgnoreResult("Name"))
	if len(rec.errors) != 0 {
		t.Errorf("ignored fields were reported:\n%s", strings.Join(rec.errors, "\n"))
	}
}
