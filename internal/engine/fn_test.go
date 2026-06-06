package engine

import (
	"context"
	"errors"
	"testing"
)

// Top-level functions so name derivation has something honest to chew on.
func BrewCoffee()                       {}
func OverthinkArchitecture() error      { return nil }
func ConsultRubberDuck(context.Context) {}
func ShipIt(context.Context) error      { return nil }
func YOLO()                             {}

func TestNormalize_AcceptsAllFourMageShapes(t *testing.T) {
	for _, v := range []any{BrewCoffee, OverthinkArchitecture, ConsultRubberDuck, ShipIt} {
		fn, err := Normalize(v)
		if err != nil {
			t.Fatalf("Normalize(%T): %v", v, err)
		}
		if err := fn.Call(context.Background()); err != nil {
			t.Fatalf("Call(%T): %v", v, err)
		}
	}
}

func TestNormalize_RejectsCreativeSignatures(t *testing.T) {
	for _, v := range []any{
		func(int) {},
		func() (string, error) { return "", nil },
		"just a string with ambitions",
		42,
	} {
		if _, err := Normalize(v); err == nil {
			t.Fatalf("Normalize(%T) accepted a dep it should have judged", v)
		}
	}
}

func TestNormalize_DerivesMageStyleNames(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{BrewCoffee, "brewCoffee"},
		{OverthinkArchitecture, "overthinkArchitecture"},
		{ShipIt, "shipIt"},
		{YOLO, "yolo"}, // initialisms lower entirely; "yOLO" helps nobody
	} {
		fn, err := Normalize(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		if fn.Name != tc.want {
			t.Fatalf("name = %q, want %q", fn.Name, tc.want)
		}
	}
}

func TestNormalize_ErrorsPassThrough(t *testing.T) {
	errNope := errors.New("nope")
	fn, err := Normalize(func() error { return errNope })
	if err != nil {
		t.Fatal(err)
	}
	if got := fn.Call(context.Background()); !errors.Is(got, errNope) {
		t.Fatalf("Call() = %v, want errNope", got)
	}
}

func TestNormalize_IdentityContract(t *testing.T) {
	// Top-level functions: one identity however many expressions reference
	// them — this is what makes targets dedup across callsites (mg.Deps
	// contract). Verified empirically on Go 1.26; this test exists so a
	// toolchain change can't quietly break dedup.
	a, _ := Normalize(BrewCoffee)
	b, _ := Normalize(BrewCoffee)
	if a.Key != b.Key {
		t.Fatal("same top-level target got two identities; dedup is broken")
	}

	// Distinct closures: two baristas are two steps, even if they trained
	// at the same literal. The same closure value, though, stays one step.
	mk := func(name string) func() { return func() { _ = name } }
	alice, _ := Normalize(mk("alice"))
	bob, _ := Normalize(mk("bob"))
	if alice.Key == bob.Key {
		t.Fatal("distinct closure instances share an identity; unrelated steps would dedup")
	}
	carol := mk("carol")
	c1, _ := Normalize(carol)
	c2, _ := Normalize(carol)
	if c1.Key != c2.Key {
		t.Fatal("the same closure value got two identities; dedup is broken")
	}
}
