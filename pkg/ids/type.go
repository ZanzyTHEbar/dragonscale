package ids

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
)

// UUID represents a 16-byte RFC-9562 UUIDv7 value.
type UUID [16]byte

// Parse converts a canonical UUID string into UUID.
func Parse(s string) (UUID, error) {
	var out UUID
	b, err := ToBytes(s)
	if err != nil {
		return out, err
	}
	copy(out[:], b[:])
	return out, nil
}

// MustParse parses and panics on error. Safe when input is known-good.
func MustParse(s string) UUID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// Bytes returns raw 16 bytes.
func (u UUID) Bytes() []byte { return u[:][:] }

// IsZero returns true if UUID is all zero bytes.
func (u UUID) IsZero() bool {
	for _, b := range u {
		if b != 0 {
			return false
		}
	}
	return true
}

// String returns canonical UUID string representation.
func (u UUID) String() string { return encodeCanonical([16]byte(u)) }

// Value implements driver.Valuer, returning raw 16-byte BLOB for storage.
// SQLite stores UUIDs as BLOB PRIMARY KEY — 16 bytes vs 36 bytes for TEXT.
// Zero UUID maps to SQL NULL (prevents accidental zero-value primary keys).
func (u UUID) Value() (driver.Value, error) {
	if u.IsZero() {
		return nil, nil
	}
	b := make([]byte, 16)
	copy(b, u[:])
	return b, nil
}

// Scan implements sql.Scanner to read UUID from database values (BLOB or TEXT).
func (u *UUID) Scan(src interface{}) error {
	if src == nil {
		// leave zero value
		return nil
	}
	switch v := src.(type) {
	case []byte:
		if len(v) != 16 {
			return fmt.Errorf("invalid uuid blob length: %d", len(v))
		}
		copy(u[:], v)
		return nil
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*u = parsed
		return nil
	default:
		return errors.New("unsupported uuid scan source type")
	}
}

// MarshalJSON encodes UUID as JSON string.
func (u UUID) MarshalJSON() ([]byte, error) {
	return json.Marshal(u.String())
}

// UnmarshalJSON decodes UUID from JSON string.
func (u *UUID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := Parse(s)
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}
