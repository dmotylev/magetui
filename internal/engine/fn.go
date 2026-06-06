package engine

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Fn is a normalized dependency: any of the four accepted function shapes
// reduced to func(context.Context) error, plus the identity key used for
// once-per-target dedup and the display metadata.
type Fn struct {
	Name string
	Icon string
	Key  uintptr
	Call func(context.Context) error
}

// Normalize converts a dependency value into an Fn. Accepted shapes mirror
// mage: func(), func() error, func(context.Context), and
// func(context.Context) error. Anything else is an error.
//
// Identity is reflect.Value.Pointer of the dep value: stable for a
// top-level function however many callsites reference it (so targets dedup
// across the whole run, the mg.Deps contract), while distinct closure
// instances are distinct steps — even when created from the same literal.
func Normalize(v any) (Fn, error) {
	fn := Fn{}
	switch f := v.(type) {
	case func(context.Context) error:
		fn.Call = f
	case func(context.Context):
		fn.Call = func(ctx context.Context) error { f(ctx); return nil }
	case func() error:
		fn.Call = func(context.Context) error { return f() }
	case func():
		fn.Call = func(context.Context) error { f(); return nil }
	default:
		return Fn{}, fmt.Errorf("not a valid dep type %T: deps must be func(), func() error, func(context.Context), or func(context.Context) error", v)
	}
	fn.Key = reflect.ValueOf(v).Pointer()
	fn.Name = nameOf(fn.Key)
	return fn, nil
}

// nameOf derives a display name from a function's code pointer, mage-style:
// package path stripped, method-value suffix removed, first rune lowered
// ("Build" → "build", "NS.Deploy" → "nS.Deploy" is avoided by lowering only
// the last path segment's first rune).
func nameOf(pc uintptr) string {
	f := runtime.FuncForPC(pc)
	if f == nil {
		return "anonymous"
	}
	name := f.Name()
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return lowerFirst(name)
}

func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
