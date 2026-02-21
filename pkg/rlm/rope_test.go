package rlm_test

import (
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/rlm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRope_EmptyRope(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("")
	assert.Equal(t, 0, r.Len())
	assert.Equal(t, "", r.String())
}

func TestRope_BasicAppendAndString(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hello")
	r.Append(" world")
	assert.Equal(t, 11, r.Len())
	assert.Equal(t, "hello world", r.String())
}

func TestRope_LargeContent(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("abcdefghij", 1000) // 10000 bytes
	r := rlm.NewRope(content)
	assert.Equal(t, 10000, r.Len())
	assert.Equal(t, content, r.String())
}

func TestRope_Slice_ValidRange(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hello world")
	s, err := r.Slice(6, 11)
	require.NoError(t, err)
	assert.Equal(t, "world", s)
}

func TestRope_Slice_ZeroLength(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hello")
	s, err := r.Slice(2, 2)
	require.NoError(t, err)
	assert.Equal(t, "", s)
}

func TestRope_Slice_OutOfRange(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hello")
	_, err := r.Slice(3, 10)
	assert.Error(t, err)
}

func TestRope_Slice_AcrossAppendBoundary(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hello")
	r.Append(" world")
	s, err := r.Slice(3, 8)
	require.NoError(t, err)
	assert.Equal(t, "lo wo", s)
}

func TestRope_Lines(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("line1\nline2\nline3")
	lines := r.Lines()
	assert.Equal(t, []string{"line1", "line2", "line3"}, lines)
}

func TestRope_GrepLines_CaseSensitive(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("apple\nBanana\napricot\ncherry")
	matches := r.GrepLines("ap", 0, false)
	require.Len(t, matches, 2)
	assert.Equal(t, 1, matches[0].LineNum)
	assert.Equal(t, 3, matches[1].LineNum)
}

func TestRope_GrepLines_CaseInsensitive(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("Apple\nbanana\nAPRICOT")
	matches := r.GrepLines("apple", 0, true)
	require.Len(t, matches, 1)
	assert.Equal(t, "Apple", matches[0].Line)
}

func TestRope_GrepLines_MaxMatches(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("aa\naa\naa\naa\naa")
	matches := r.GrepLines("aa", 3, false)
	assert.Len(t, matches, 3)
}

func TestRope_GrepLines_NoMatches(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hello world")
	matches := r.GrepLines("xyz", 0, false)
	assert.Empty(t, matches)
}

func TestRope_Partition_Even(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("12345678")
	parts := r.Partition(4)
	assert.Len(t, parts, 4)
	assert.Equal(t, "12345678", strings.Join(parts, ""))
}

func TestRope_Partition_MoreThanContent(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("hi")
	parts := r.Partition(10)
	assert.Len(t, parts, 10)
	// All content should appear in first non-empty partition.
	combined := strings.Join(parts, "")
	assert.Equal(t, "hi", combined)
}

func TestRope_Partition_Empty(t *testing.T) {
	t.Parallel()
	r := rlm.NewRope("")
	parts := r.Partition(4)
	assert.Len(t, parts, 4)
	for _, p := range parts {
		assert.Equal(t, "", p)
	}
}

func TestRope_RuneLen(t *testing.T) {
	t.Parallel(
	// Multi-byte Unicode characters.
	)

	r := rlm.NewRope("héllo") // 'é' is 2 bytes
	assert.Equal(t, 5, r.RuneLen())
	assert.Equal(t, 6, r.Len()) // bytes
}
