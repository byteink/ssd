package cleanup

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- PruneOldTags (orchestrator) ---

// fakeCleaner is a hand-rolled ImageCleaner recording calls — simpler
// than mockery for the few behavioural tests PruneOldTags needs.
type fakeCleaner struct {
	listTags    func(string) ([]Tag, error)
	removed     []string
	removeErr   error
}

func (f *fakeCleaner) ListTags(_ context.Context, image string) ([]Tag, error) {
	return f.listTags(image)
}

func (f *fakeCleaner) RemoveImage(_ context.Context, ref string) error {
	f.removed = append(f.removed, ref)
	return f.removeErr
}

func (f *fakeCleaner) PruneBuildCache(_ context.Context) error { return nil }
func (f *fakeCleaner) PruneDangling(_ context.Context) error   { return nil }

func TestPruneOldTags_RemovesOldKeepsRunningAndTopN(t *testing.T) {
	f := &fakeCleaner{listTags: func(string) ([]Tag, error) {
		return []Tag{{Numeric: 5}, {Numeric: 4}, {Numeric: 3}, {Numeric: 2}, {Numeric: 1}}, nil
	}}

	removed, err := PruneOldTags(context.Background(), f, "ssd-foo-web", 2, 5)
	require.NoError(t, err)

	// Keep 5 (running + top1) + 4 (top2). Remove 3, 2, 1.
	assert.Equal(t, []string{"ssd-foo-web:3", "ssd-foo-web:2", "ssd-foo-web:1"}, f.removed)
	assert.Len(t, removed, 3)
}

func TestPruneOldTags_RetentionZeroIsNoOp(t *testing.T) {
	f := &fakeCleaner{listTags: func(string) ([]Tag, error) {
		t.Fatal("ListTags must not be called when keep=0")
		return nil, nil
	}}
	removed, err := PruneOldTags(context.Background(), f, "ssd-foo-web", 0, 5)
	require.NoError(t, err)
	assert.Empty(t, removed)
	assert.Empty(t, f.removed)
}

func TestPruneOldTags_ContinuesOnRemoveFailure(t *testing.T) {
	// One tag failing to remove shouldn't stop the rest — best-effort
	// cleanup is the whole point of warn-only deploy hook.
	f := &fakeCleaner{
		listTags:  func(string) ([]Tag, error) { return []Tag{{Numeric: 3}, {Numeric: 2}, {Numeric: 1}}, nil },
		removeErr: errors.New("in use by running container"),
	}
	_, err := PruneOldTags(context.Background(), f, "ssd-foo-web", 1, 3)
	require.NoError(t, err, "PruneOldTags must swallow per-tag errors")
	// Attempted all three even though each "failed".
	assert.Equal(t, []string{"ssd-foo-web:2", "ssd-foo-web:1"}, f.removed)
}

func TestPruneOldTags_PropagatesListError(t *testing.T) {
	f := &fakeCleaner{listTags: func(string) ([]Tag, error) {
		return nil, errors.New("ssh down")
	}}
	_, err := PruneOldTags(context.Background(), f, "ssd-foo-web", 2, 5)
	require.Error(t, err, "ListTags failures must surface to the caller")
}

func TestNewCleaner_SelectsByRuntime(t *testing.T) {
	_, composeOK := NewCleaner("compose", nil).(*ComposeCleaner)
	assert.True(t, composeOK, "compose runtime must return *ComposeCleaner")

	_, k3sOK := NewCleaner("k3s", nil).(*K3sCleaner)
	assert.True(t, k3sOK, "k3s runtime must return *K3sCleaner")

	// Unknown runtime falls back to compose (least-surprise, matches
	// config default).
	_, fallback := NewCleaner("unknown", nil).(*ComposeCleaner)
	assert.True(t, fallback, "unknown runtime must fall back to compose")
}

func numeric(ts []Tag) []int {
	out := make([]int, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Numeric)
	}
	return out
}
