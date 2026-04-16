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

func TestK3sCleaner_ListTags_FiltersByPrefixClientSide(t *testing.T) {
	// nerdctl doesn't support name-filter — we list all and filter in Go.
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "nerdctl --namespace k8s.io images")
	})).Return(`<none>:<none>
ssd-byteink-website-web:3
ssd-byteink-website-web:2
<none>:<none>
ssd-byteink-website-web:1
ssd-thinkbyte-web:3
ghcr.io/byteink/mcpforest-api:<none>
ssd-iaaa-web:1
`, nil)

	cleaner := NewK3sCleaner(client)
	tags, err := cleaner.ListTags(context.Background(), "ssd-byteink-website-web")
	require.NoError(t, err)

	// Only the byteink-website-web tags — thinkbyte-web, iaaa-web,
	// ghcr.io/*, and <none> entries are dropped.
	assert.ElementsMatch(t, []Tag{
		{Numeric: 3},
		{Numeric: 2},
		{Numeric: 1},
	}, tags)
}

func TestK3sCleaner_ListTags_EmptyOutput(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.Anything).Return("", nil)

	cleaner := NewK3sCleaner(client)
	tags, err := cleaner.ListTags(context.Background(), "ssd-foo-web")
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestK3sCleaner_ListTags_PropagatesSSHError(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.Anything).Return("", errors.New("ssh failed"))

	cleaner := NewK3sCleaner(client)
	_, err := cleaner.ListTags(context.Background(), "ssd-foo-web")
	require.Error(t, err)
}

func TestK3sCleaner_RemoveImage_RunsNerdctlRmi(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "nerdctl --namespace k8s.io rmi") &&
			strings.Contains(cmd, "ssd-foo-web:3")
	})).Return("", nil)

	cleaner := NewK3sCleaner(client)
	err := cleaner.RemoveImage(context.Background(), "ssd-foo-web:3")
	require.NoError(t, err)
	client.AssertExpectations(t)
}

func TestK3sCleaner_RemoveImage_RejectsForeignRef(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	cleaner := NewK3sCleaner(client)

	err := cleaner.RemoveImage(context.Background(), "nginx:latest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing")
	client.AssertNotCalled(t, "SSH", mock.Anything)
}

func TestK3sCleaner_PruneBuildCache_RunsBuildctl(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "buildctl") &&
			strings.Contains(cmd, "unix:///run/buildkit/buildkitd.sock") &&
			strings.Contains(cmd, "prune") &&
			strings.Contains(cmd, "168h")
	})).Return("", nil)

	cleaner := NewK3sCleaner(client)
	err := cleaner.PruneBuildCache(context.Background())
	require.NoError(t, err)
	client.AssertExpectations(t)
}

func TestK3sCleaner_PruneDangling_RunsNerdctlImagePrune(t *testing.T) {
	client := &testhelpers.MockRemoteClient{}
	client.On("SSH", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "nerdctl --namespace k8s.io image prune")
	})).Return("", nil)

	cleaner := NewK3sCleaner(client)
	err := cleaner.PruneDangling(context.Background())
	require.NoError(t, err)
	client.AssertExpectations(t)
}
