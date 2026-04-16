package cleanup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestComposeCleaner_ListTags_ParsesNumericAndIgnoresOthers(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "docker images") && strings.Contains(cmd, "ssd-byteink-website-web")
	})).Return("ssd-byteink-website-web:3\nssd-byteink-website-web:2\nssd-byteink-website-web:1\nssd-byteink-website-web:latest\n", nil)

	cleaner := NewComposeCleaner(client)
	tags, err := cleaner.ListTags(context.Background(), "ssd-byteink-website-web")
	require.NoError(t, err)

	// Numeric tags surface with Numeric set; non-numeric carry Raw only.
	assert.ElementsMatch(t, []Tag{
		{Numeric: 3},
		{Numeric: 2},
		{Numeric: 1},
		{Raw: "latest"},
	}, tags)
}

func TestComposeCleaner_ListTags_SkipsNoneEntries(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.Anything).Return("ssd-foo-web:2\n<none>:<none>\nssd-foo-web:1\n", nil)

	cleaner := NewComposeCleaner(client)
	tags, err := cleaner.ListTags(context.Background(), "ssd-foo-web")
	require.NoError(t, err)

	assert.ElementsMatch(t, []Tag{{Numeric: 2}, {Numeric: 1}}, tags)
}

func TestComposeCleaner_ListTags_EmptyOutput(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.Anything).Return("", nil)

	cleaner := NewComposeCleaner(client)
	tags, err := cleaner.ListTags(context.Background(), "ssd-foo-web")
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestComposeCleaner_ListTags_PropagatesSSHError(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.Anything).Return("", errors.New("ssh failed"))

	cleaner := NewComposeCleaner(client)
	_, err := cleaner.ListTags(context.Background(), "ssd-foo-web")
	require.Error(t, err)
}

func TestComposeCleaner_RemoveImage_RunsDockerRmi(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "docker rmi") && strings.Contains(cmd, "ssd-foo-web:3")
	})).Return("", nil)

	cleaner := NewComposeCleaner(client)
	err := cleaner.RemoveImage(context.Background(), "ssd-foo-web:3")
	require.NoError(t, err)
	client.AssertExpectations(t)
}

func TestComposeCleaner_PruneBuildCache_RunsBuilderPrune(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "docker builder prune") && strings.Contains(cmd, "until=168h")
	})).Return("Total reclaimed space: 29.6GB\n", nil)

	cleaner := NewComposeCleaner(client)
	err := cleaner.PruneBuildCache(context.Background())
	require.NoError(t, err)
	client.AssertExpectations(t)
}

func TestComposeCleaner_PruneDangling_RunsImagePrune(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "docker image prune")
	})).Return("Total reclaimed space: 1.2GB\n", nil)

	cleaner := NewComposeCleaner(client)
	err := cleaner.PruneDangling(context.Background())
	require.NoError(t, err)
	client.AssertExpectations(t)
}

// Rejects image references outside the ssd-<project>-<service> prefix.
// Belt-and-braces: SelectOldTags already filters, but RemoveImage must
// never touch foreign images even if called directly.
func TestComposeCleaner_RemoveImage_RejectsForeignRef(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	cleaner := NewComposeCleaner(client)

	err := cleaner.RemoveImage(context.Background(), "nginx:latest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing")
	// Should never have called SSH.
	client.AssertNotCalled(t, "SSH", mock.Anything)
}

