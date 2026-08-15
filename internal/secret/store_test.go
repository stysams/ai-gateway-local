package secret

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestMemStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()

	if err := s.Put(ctx, "provider.openrouter", []byte("sk-test-value")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "provider.openrouter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer Zero(got)
	if string(got) != "sk-test-value" {
		t.Errorf("Get = %q, want %q", got, "sk-test-value")
	}

	// Put replaces the previous value.
	if err := s.Put(ctx, "provider.openrouter", []byte("sk-new")); err != nil {
		t.Fatalf("Put replace: %v", err)
	}
	got2, err := s.Get(ctx, "provider.openrouter")
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	defer Zero(got2)
	if string(got2) != "sk-new" {
		t.Errorf("Get after replace = %q, want %q", got2, "sk-new")
	}
}

func TestMemStoreGetMissing(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	if _, err := s.Get(ctx, "provider.never"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}

func TestMemStoreDelete(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	if err := s.Put(ctx, "provider.x", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "provider.x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "provider.x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	// Deleting a missing ref is a no-op, not an error.
	if err := s.Delete(ctx, "provider.x"); err != nil {
		t.Errorf("Delete missing = %v, want nil", err)
	}
}

func TestMemStoreList(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	for _, ref := range []string{"provider.a", "provider.b", "provider.c"} {
		if err := s.Put(ctx, ref, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(refs)
	if want := []string{"provider.a", "provider.b", "provider.c"}; !reflect.DeepEqual(refs, want) {
		t.Errorf("List = %v, want %v", refs, want)
	}
	if err := s.Delete(ctx, "provider.b"); err != nil {
		t.Fatal(err)
	}
	refs, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Errorf("List after delete = %v, want 2 refs", refs)
	}
}

func TestValidRef(t *testing.T) {
	cases := []struct {
		ref    string
		wantOK bool
	}{
		{"provider.openrouter", true},
		{"a", true},
		{"a1", true},
		{"A-b_c.d", true},
		{"a..b", true},
		{"", false},
		{"provider/with/slash", false},
		{"provider with space", false},
		{"provider\\backslash", false},
		{"..", false}, // must not start with '.' (path traversal risk)
		{".hidden", false},
		{"-leading-dash", false},
	}
	for _, tc := range cases {
		err := ValidRef(tc.ref)
		if (err == nil) != tc.wantOK {
			t.Errorf("ValidRef(%q) = %v, want ok=%v", tc.ref, err, tc.wantOK)
		}
	}
}

func TestZero(t *testing.T) {
	b := []byte("sensitive")
	Zero(b)
	for i, v := range b {
		if v != 0 {
			t.Errorf("b[%d] = %d, want 0", i, v)
		}
	}
}

func TestMemStoreValidatesRef(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	if err := s.Put(ctx, "bad/ref", []byte("v")); err == nil {
		t.Error("Put accepted invalid ref")
	}
	if _, err := s.Get(ctx, "bad/ref"); err == nil {
		t.Error("Get accepted invalid ref")
	}
	if err := s.Delete(ctx, "bad/ref"); err == nil {
		t.Error("Delete accepted invalid ref")
	}
}

func TestMemStoreGetReturnsFreshCopy(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	if err := s.Put(ctx, "provider.x", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	a, err := s.Get(ctx, "provider.x")
	if err != nil {
		t.Fatal(err)
	}
	defer Zero(a)
	b, err := s.Get(ctx, "provider.x")
	if err != nil {
		t.Fatal(err)
	}
	defer Zero(b)
	if &a[0] == &b[0] {
		t.Error("Get returned the same backing array twice; callers must be able to zero their copy")
	}
}

func TestMemStoreAvailable(t *testing.T) {
	if err := NewMemStore().Available(context.Background()); err != nil {
		t.Errorf("Available = %v, want nil", err)
	}
}
