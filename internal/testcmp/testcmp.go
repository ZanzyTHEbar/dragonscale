package testcmp

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func RequireEqual[T any](t testing.TB, want, got T, opts ...cmp.Option) {
	t.Helper()
	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Fatalf("unexpected diff (-want +got):\n%s", diff)
	}
}

func AssertEqual[T any](t testing.TB, want, got T, opts ...cmp.Option) bool {
	t.Helper()
	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Errorf("unexpected diff (-want +got):\n%s", diff)
		return false
	}
	return true
}
