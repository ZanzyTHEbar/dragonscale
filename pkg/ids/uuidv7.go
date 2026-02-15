package ids

import (
	"crypto/rand"
	"time"
)

// New returns a RFC 9562 UUID version 7 as UUID type.
// Layout:
// - 48-bit big-endian Unix milliseconds timestamp
// - 4-bit version (0b0111)
// - 12-bit randomness (rand_a)
// - 2-bit variant (0b10)
// - 62-bit randomness (rand_b)
func New() UUID {
	var u UUID

	// 48-bit timestamp (ms since Unix epoch), big-endian
	ms := uint64(time.Now().UnixMilli())
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)

	// Fill remaining bytes with randomness
	_, _ = rand.Read(u[6:])

	// Set version (0b0111 in high nibble of byte 6)
	u[6] = (u[6] & 0x0f) | 0x70
	// Set variant (0b10 in high bits of byte 8)
	u[8] = (u[8] & 0x3f) | 0x80

	return u
}

// encodeCanonical renders the UUID bytes as 8-4-4-4-12 hexadecimal groups.
func encodeCanonical(u [16]byte) string {
	var dst [36]byte
	hex := func(b byte) (byte, byte) {
		const hexdigits = "0123456789abcdef"
		return hexdigits[b>>4], hexdigits[b&0x0f]
	}
	writeByte := func(off int, b byte) int {
		h, l := hex(b)
		dst[off] = h
		dst[off+1] = l
		return off + 2
	}

	o := 0
	// 4 bytes -> 8 chars
	for i := 0; i < 4; i++ {
		o = writeByte(o, u[i])
	}
	dst[o] = '-'
	o++
	// 2 bytes -> 4 chars
	for i := 4; i < 6; i++ {
		o = writeByte(o, u[i])
	}
	dst[o] = '-'
	o++
	// 2 bytes -> 4 chars
	for i := 6; i < 8; i++ {
		o = writeByte(o, u[i])
	}
	dst[o] = '-'
	o++
	// 2 bytes -> 4 chars
	for i := 8; i < 10; i++ {
		o = writeByte(o, u[i])
	}
	dst[o] = '-'
	o++
	// 6 bytes -> 12 chars
	for i := 10; i < 16; i++ {
		o = writeByte(o, u[i])
	}
	return string(dst[:])
}
