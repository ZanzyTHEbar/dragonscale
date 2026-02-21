package ids

import (
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
)

func TestNew_IsV7(t *testing.T) {
	t.Parallel()
	u := New()

	if u.IsZero() {
		t.Fatal("New() produced zero UUID")
	}

	// Version nibble (byte 6, high nibble) must be 0x7
	if v := u[6] >> 4; v != 7 {
		t.Fatalf("expected version 7, got %d", v)
	}
	// Variant bits (byte 8, top 2 bits) must be 0b10
	if v := u[8] >> 6; v != 2 {
		t.Fatalf("expected variant 0b10, got %d", v)
	}
}

func TestNew_Unique(t *testing.T) {
	t.Parallel()
	seen := make(map[UUID]bool, 1000)
	for i := 0; i < 1000; i++ {
		u := New()
		if seen[u] {
			t.Fatalf("duplicate UUID at iteration %d: %s", i, u.String())
		}
		seen[u] = true
	}
}

func TestNew_Monotonic(t *testing.T) {
	t.Parallel()
	a := New()
	b := New()
	// UUIDv7 embeds ms timestamp in first 6 bytes. b >= a in timestamp.
	for i := 0; i < 6; i++ {
		if a[i] < b[i] {
			return // a < b, correct
		}
		if a[i] > b[i] {
			t.Fatalf("non-monotonic: a=%s b=%s", a.String(), b.String())
		}
	}
	// Equal timestamps are fine (same millisecond)
}

func TestParse_RoundTrip(t *testing.T) {
	t.Parallel()
	u := New()
	s := u.String()

	parsed, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	if parsed != u {
		t.Fatalf("round-trip failed: %s != %s", parsed.String(), s)
	}
}

func TestParse_Errors(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"not-a-uuid",
		"12345678-1234-1234-1234",
		"zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz",
		"0000000000000000000000000000000000", // 34 chars, no hyphens, wrong length with hyphens
	}
	for _, s := range cases {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) expected error", s)
		}
	}
}

func TestIsZero(t *testing.T) {
	t.Parallel()
	var zero UUID
	if !zero.IsZero() {
		t.Fatal("zero UUID should be zero")
	}
	u := New()
	if u.IsZero() {
		t.Fatal("New() UUID should not be zero")
	}
}

func TestValue_BlobRoundTrip(t *testing.T) {
	t.Parallel()
	u := New()
	v, err := u.Value()
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	b, ok := v.([]byte)
	if !ok {
		t.Fatalf("Value() returned %T, want []byte", v)
	}
	if len(b) != 16 {
		t.Fatalf("Value() returned %d bytes, want 16", len(b))
	}

	var scanned UUID
	if err := scanned.Scan(b); err != nil {
		t.Fatalf("Scan(blob): %v", err)
	}
	if scanned != u {
		t.Fatalf("BLOB round-trip: %s != %s", scanned.String(), u.String())
	}
}

func TestValue_ZeroIsNil(t *testing.T) {
	t.Parallel()
	var zero UUID
	v, err := zero.Value()
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if v != nil {
		t.Fatalf("zero UUID Value() should be nil, got %v", v)
	}
}

func TestScan_NilLeavesZero(t *testing.T) {
	t.Parallel()
	var u UUID
	if err := u.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if !u.IsZero() {
		t.Fatalf("Scan(nil) should leave zero UUID, got %s", u.String())
	}
}

func TestScan_String(t *testing.T) {
	t.Parallel()
	orig := New()
	var u UUID
	if err := u.Scan(orig.String()); err != nil {
		t.Fatalf("Scan(string): %v", err)
	}
	if u != orig {
		t.Fatalf("Scan(string): %s != %s", u.String(), orig.String())
	}
}

func TestScan_InvalidBlob(t *testing.T) {
	t.Parallel()
	var u UUID
	if err := u.Scan([]byte{1, 2, 3}); err == nil {
		t.Fatal("Scan(3-byte blob) should fail")
	}
}

func TestScan_InvalidType(t *testing.T) {
	t.Parallel()
	var u UUID
	if err := u.Scan(42); err == nil {
		t.Fatal("Scan(int) should fail")
	}
}

func TestJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	u := New()

	b, err := jsonv2.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Should be a quoted string
	var s string
	if err := jsonv2.Unmarshal(b, &s); err != nil {
		t.Fatalf("Unmarshal to string: %v", err)
	}
	if s != u.String() {
		t.Fatalf("JSON string mismatch: %q != %q", s, u.String())
	}

	var parsed UUID
	if err := jsonv2.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed != u {
		t.Fatalf("JSON round-trip: %s != %s", parsed.String(), u.String())
	}
}

func TestJSON_ZeroUUID(t *testing.T) {
	t.Parallel()
	var zero UUID
	b, err := jsonv2.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal zero: %v", err)
	}
	// Zero UUID should still serialize as a valid UUID string
	if string(b) != `"00000000-0000-0000-0000-000000000000"` {
		t.Fatalf("zero UUID JSON: %s", string(b))
	}
}

func TestJSON_InStruct(t *testing.T) {
	t.Parallel()
	type record struct {
		ID   UUID   `json:"id"`
		Name string `json:"name"`
	}

	r := record{ID: New(), Name: "test"}
	b, err := jsonv2.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal struct: %v", err)
	}

	var decoded record
	if err := jsonv2.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal struct: %v", err)
	}
	if decoded.ID != r.ID {
		t.Fatalf("struct round-trip: %s != %s", decoded.ID.String(), r.ID.String())
	}
}

func TestFromBytes(t *testing.T) {
	t.Parallel()
	u := New()
	b := u.Bytes()
	restored := FromBytes(b)
	if restored != u {
		t.Fatalf("FromBytes: %s != %s", restored.String(), u.String())
	}
}

func TestMustParse_Panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustParse should panic on bad input")
		}
	}()
	MustParse("garbage")
}

func TestString_Format(t *testing.T) {
	t.Parallel()
	u := New()
	s := u.String()
	if len(s) != 36 {
		t.Fatalf("String() length %d, want 36", len(s))
	}
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		t.Fatalf("String() missing hyphens: %s", s)
	}
}

func TestUUIDComparable(t *testing.T) {
	t.Parallel()
	a := New()
	b := a // copy
	if a != b {
		t.Fatal("copied UUIDs should be equal")
	}
	c := New()
	if a == c {
		t.Fatal("different UUIDs should not be equal")
	}
}
