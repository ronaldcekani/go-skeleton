// Package mapcheck tests that a function mapping one struct to another loses
// nothing. Mappers are written by hand in this codebase, and the compiler
// does not notice when a new field is left out of one; these checks do.
//
// It is meant for tests only.
package mapcheck

import (
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"time"
)

// T is the part of *testing.T the checks need.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

type options struct {
	ignoreSource []string
	ignoreResult []string
}

type Option func(*options)

// IgnoreSource names source fields the mapper leaves out on purpose, for
// example "PasswordHash" or, for a nested field, "Lines.Note".
func IgnoreSource(paths ...string) Option {
	return func(o *options) { o.ignoreSource = append(o.ignoreSource, paths...) }
}

// IgnoreResult names result fields the mapper leaves empty on purpose.
func IgnoreResult(paths ...string) Option {
	return func(o *options) { o.ignoreResult = append(o.ignoreResult, paths...) }
}

// Check calls mapper with generated values of S and fails the test if:
//
//   - a field of the result is empty although every field of the source is
//     set: the mapper does not fill it (typically a field added to D);
//   - changing one field of the source leaves the result unchanged: the
//     mapper does not read it (typically a field added to S).
//
// A field that is left out on purpose has to be named with IgnoreSource or
// IgnoreResult, which turns an oversight into a visible decision.
//
// Generated strings are decimal numbers, so a mapper that parses a string
// must accept those. Results are compared with reflect.DeepEqual.
func Check[S, D any](t T, mapper func(S) D, opts ...Option) {
	t.Helper()
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	source, fields := generate[S](t, -1)
	result := mapper(source)

	for _, path := range emptyFields(reflect.ValueOf(&result).Elem(), "") {
		if !slices.Contains(o.ignoreResult, path) {
			t.Errorf("%T.%s is empty: the mapper from %T does not fill it", result, path, source)
		}
	}
	for i, path := range fields {
		if slices.Contains(o.ignoreSource, path) {
			continue
		}
		changed, _ := generate[S](t, i)
		if reflect.DeepEqual(mapper(changed), result) {
			t.Errorf("%T.%s is ignored: changing it does not change the %T", source, path, result)
		}
	}
}

// generate returns an S with every field set, and the paths of its fields in
// the order they were set. The field with index alter gets a different value
// than in the S generated with alter = -1.
func generate[S any](t T, alter int) (S, []string) {
	t.Helper()
	var s S
	g := &generator{t: t, alter: alter}
	g.fill(reflect.ValueOf(&s).Elem(), "")
	if len(g.paths) == 0 {
		t.Fatalf("mapcheck: %T has no fields to check", s)
	}
	return s, g.paths
}

type generator struct {
	t     T
	alter int
	paths []string
}

var (
	timeType = reflect.TypeFor[time.Time]()
	ratType  = reflect.TypeFor[big.Rat]()
)

// seed returns the number the next field's value is made from.
func (g *generator) seed(path string) int64 {
	n := int64(len(g.paths) + 1)
	if len(g.paths) == g.alter {
		n += 1000
	}
	g.paths = append(g.paths, path)
	return n
}

func (g *generator) fill(v reflect.Value, path string) {
	g.t.Helper()
	switch {
	case v.Type() == timeType:
		n := g.seed(path)
		v.Set(reflect.ValueOf(time.Unix(1_700_000_000+n*3600, n*1000).UTC()))
		return
	case v.Type() == ratType:
		v.Set(reflect.ValueOf(*big.NewRat(g.seed(path), 7)))
		return
	}

	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(g.seed(path) < 1000)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(g.seed(path))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(g.seed(path)))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(g.seed(path)) + 0.5)
	case reflect.String:
		v.SetString(strconv.FormatInt(g.seed(path), 10))
	case reflect.Array:
		// One field, like a UUID: every element gets the same value.
		n := g.seed(path)
		for i := range v.Len() {
			if k := v.Index(i).Kind(); k >= reflect.Uint && k <= reflect.Uint64 {
				v.Index(i).SetUint(uint64(n%251) + 1)
			} else if k >= reflect.Int && k <= reflect.Int64 {
				v.Index(i).SetInt(n%127 + 1)
			} else {
				g.t.Fatalf("mapcheck: cannot generate %s (%s)", path, v.Type())
			}
		}
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		g.fill(v.Elem(), path)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		g.fill(v.Index(0), path)
	case reflect.Struct:
		exported := 0
		for i := range v.NumField() {
			if f := v.Type().Field(i); f.IsExported() {
				exported++
				g.fill(v.Field(i), join(path, f.Name))
			}
		}
		if exported == 0 {
			g.t.Fatalf("mapcheck: cannot generate %s (%s has no exported fields)", path, v.Type())
		}
	default:
		g.t.Fatalf("mapcheck: cannot generate %s (%s)", path, v.Type())
	}
}

// emptyFields returns the paths of the zero-valued fields of v.
func emptyFields(v reflect.Value, path string) []string {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return []string{path}
		}
		return emptyFields(v.Elem(), path)
	case reflect.Slice, reflect.Map:
		if v.Len() == 0 {
			return []string{path}
		}
		if v.Kind() == reflect.Map {
			return nil
		}
		var empty []string
		for i := range v.Len() {
			empty = append(empty, emptyFields(v.Index(i), path)...)
		}
		return empty
	case reflect.Struct:
		var empty []string
		exported := 0
		for i := range v.NumField() {
			if f := v.Type().Field(i); f.IsExported() {
				exported++
				empty = append(empty, emptyFields(v.Field(i), join(path, f.Name))...)
			}
		}
		// time.Time, big.Rat and the like: only the whole value can be judged.
		if exported == 0 && v.IsZero() {
			return []string{path}
		}
		return empty
	default:
		if v.IsZero() {
			return []string{path}
		}
		return nil
	}
}

func join(path, field string) string {
	if path == "" {
		return field
	}
	return fmt.Sprintf("%s.%s", path, field)
}
