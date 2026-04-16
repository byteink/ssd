package cleanup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectOldTags_KeepTopN(t *testing.T) {
	tags := []Tag{
		{Numeric: 59}, {Numeric: 57}, {Numeric: 56},
		{Numeric: 55}, {Numeric: 52}, {Numeric: 50},
	}
	old := SelectOldTags(tags, 2, 59)
	// keep 59 (running) + 57 (top-N), remove the rest
	assert.Equal(t, []int{56, 55, 52, 50}, numeric(old))
}

func TestSelectOldTags_KeepOneProtectsRunning(t *testing.T) {
	tags := []Tag{{Numeric: 3}, {Numeric: 2}, {Numeric: 1}}
	old := SelectOldTags(tags, 1, 3)
	// keep=1 + running protection: running is 3, remove 2 and 1
	assert.Equal(t, []int{2, 1}, numeric(old))
}

func TestSelectOldTags_NeverRemovesRunning(t *testing.T) {
	// Degenerate case: running tag is not the highest numeric.
	// Could happen after manual rollback. Running must still be protected.
	tags := []Tag{{Numeric: 5}, {Numeric: 4}, {Numeric: 3}, {Numeric: 2}}
	old := SelectOldTags(tags, 1, 3)
	// keep=1 → would normally keep [5]. Running=3 must also be kept.
	for _, t2 := range old {
		assert.NotEqual(t, 3, t2.Numeric, "running tag must not be removed")
	}
}

func TestSelectOldTags_IgnoresNonNumeric(t *testing.T) {
	tags := []Tag{
		{Numeric: 5}, {Numeric: 4},
		{Raw: "<none>"},            // intermediate layer
		{Raw: "latest"},             // non-numeric user tag
		{Numeric: 3}, {Numeric: 2},
	}
	old := SelectOldTags(tags, 2, 5)
	// keep 5 + 4, remove 3 + 2, ignore non-numeric
	assert.Equal(t, []int{3, 2}, numeric(old))
}

func TestSelectOldTags_EmptyInput(t *testing.T) {
	assert.Empty(t, SelectOldTags(nil, 2, 1))
	assert.Empty(t, SelectOldTags([]Tag{}, 2, 1))
}

func TestSelectOldTags_KeepExceedsTags(t *testing.T) {
	tags := []Tag{{Numeric: 3}, {Numeric: 2}, {Numeric: 1}}
	old := SelectOldTags(tags, 10, 3)
	assert.Empty(t, old)
}

func TestSelectOldTags_ZeroKeepTreatedAsOne(t *testing.T) {
	// keep=0 would mean "remove everything" — we clamp to 1 for safety.
	tags := []Tag{{Numeric: 3}, {Numeric: 2}, {Numeric: 1}}
	old := SelectOldTags(tags, 0, 3)
	// running still protected; treat as keep=1
	assert.Equal(t, []int{2, 1}, numeric(old))
}

func TestSelectOldTags_DuplicatesNormalized(t *testing.T) {
	tags := []Tag{{Numeric: 5}, {Numeric: 5}, {Numeric: 4}, {Numeric: 3}}
	old := SelectOldTags(tags, 2, 5)
	// keep 5 + 4, remove 3 — duplicates collapse
	assert.Equal(t, []int{3}, numeric(old))
}

func numeric(ts []Tag) []int {
	out := make([]int, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Numeric)
	}
	return out
}
