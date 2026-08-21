package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// secretDeployer is a Deployer that also exposes a k3s-style secret store.
// resolveBuildArgs must prefer it for ${secret:} refs.
type secretDeployer struct {
	*MockDeployer
	secrets string
	err     error
}

func (s *secretDeployer) ListSecrets(_ context.Context, _ string) (string, error) {
	return s.secrets, s.err
}

func buildArgCfg(args map[string]string) *config.Config {
	cfg := newTestConfig()
	cfg.Name = "api"
	cfg.BuildArgs = args
	return cfg
}

func TestResolveBuildArgs_NoArgsTouchesNothing(t *testing.T) {
	m := new(MockDeployer)
	values, _, redact, err := resolveBuildInputs(context.Background(), m, buildArgCfg(nil))

	require.NoError(t, err)
	assert.Empty(t, values)
	assert.Empty(t, redact)
	m.AssertNotCalled(t, "GetEnvFile")
}

// A literal-only set needs no server round trip at all.
func TestResolveBuildArgs_LiteralsOnly(t *testing.T) {
	m := new(MockDeployer)
	values, _, redact, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"BUILD_CHANNEL": "stable"}))

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"BUILD_CHANNEL": "stable"}, values)
	assert.Empty(t, redact, "a literal from the config is not a secret to redact")
	m.AssertNotCalled(t, "GetEnvFile")
}

func TestResolveBuildArgs_EnvRefFromEnvFile(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("# comment\nAPI_URL=https://x.test\nOTHER=nope\n", nil)

	values, _, redact, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"API_URL": "${env:API_URL}"}))

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"API_URL": "https://x.test"}, values)
	assert.Equal(t, []string{"https://x.test"}, redact)
	m.AssertNumberOfCalls(t, "GetEnvFile", 1)
}

// Two refs into the same store must cost one fetch, not one per key.
func TestResolveBuildArgs_EnvFileFetchedOnce(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("A=1\nB=2\n", nil)

	values, _, _, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"A": "${env:A}", "B": "${env:B}"}))

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"A": "1", "B": "2"}, values)
	m.AssertNumberOfCalls(t, "GetEnvFile", 1)
}

func TestResolveBuildArgs_SecretRefFromK3sSecret(t *testing.T) {
	m := new(MockDeployer)
	client := &secretDeployer{MockDeployer: m, secrets: "MAXMIND_LICENSE_KEY=abc123xyz\n"}

	values, _, redact, err := resolveBuildInputs(context.Background(), client,
		buildArgCfg(map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}))

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"MAXMIND_LICENSE_KEY": "abc123xyz"}, values)
	assert.Equal(t, []string{"abc123xyz"}, redact)
	m.AssertNotCalled(t, "GetEnvFile", "the k3s secret store answers ${secret:}")
}

// Compose has no secret store; ${secret:} falls back to the service env file
// so one ssd.yaml works across both runtimes.
func TestResolveBuildArgs_SecretRefFallsBackToEnvFileOnCompose(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("MAXMIND_LICENSE_KEY=abc123xyz\n", nil)

	values, _, _, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}))

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"MAXMIND_LICENSE_KEY": "abc123xyz"}, values)
}

func TestResolveBuildArgs_MissingEnvKeyIsHardError(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("OTHER=1\n", nil)

	_, _, _, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"API_URL": "${env:API_URL}"}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API_URL", "the error must name the missing key")
	assert.Contains(t, err.Error(), "api.env", "the error must name where it looked")
}

func TestResolveBuildArgs_MissingSecretKeyIsHardError(t *testing.T) {
	client := &secretDeployer{MockDeployer: new(MockDeployer), secrets: "OTHER=1\n"}

	_, _, _, err := resolveBuildInputs(context.Background(), client,
		buildArgCfg(map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAXMIND_LICENSE_KEY")
	assert.Contains(t, err.Error(), "api-secret", "the error must name the secret it looked in")
}

// An empty stored value is indistinguishable from a typo'd key at build time,
// so it must abort rather than bake an empty credential into the image.
func TestResolveBuildArgs_EmptyStoredValueIsHardError(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("API_URL=\n", nil)

	_, _, _, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"API_URL": "${env:API_URL}"}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API_URL")
}

// Secrecy: the resolver reads credentials, so its errors are the most likely
// accidental leak path.
func TestResolveBuildArgs_ErrorNeverContainsAnyValue(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("PRESENT=super-secret-value\n", nil)

	_, _, _, err := resolveBuildInputs(context.Background(), m, buildArgCfg(map[string]string{
		"PRESENT": "${env:PRESENT}",
		"MISSING": "${env:MISSING}",
	}))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-value")
}

func TestResolveBuildArgs_StoreReadFailurePropagates(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("", errors.New("ssh died"))

	_, _, _, err := resolveBuildInputs(context.Background(), m,
		buildArgCfg(map[string]string{"API_URL": "${env:API_URL}"}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh died")
}

func TestParseKVLines(t *testing.T) {
	got := parseKVLines("A=1\n\n# comment\nB=two=parts\nnoequals\nC=\n")
	assert.Equal(t, map[string]string{"A": "1", "B": "two=parts", "C": ""}, got)
}

func TestBuildArgKeys_SortedForStableOutput(t *testing.T) {
	assert.Equal(t, []string{"A", "B", "C"},
		buildArgKeys(map[string]string{"C": "3", "A": "1", "B": "2"}))
}

func TestDeploy_BuildArgsResolvedAndPassedToBuild(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()
	cfg.BuildArgs = map[string]string{
		"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}",
		"BUILD_CHANNEL":       "stable",
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("GetEnvFile", "myapp").Return("MAXMIND_LICENSE_KEY=abc123xyz\n", nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateManifest", 1).Return(nil)
	mockClient.On("RolloutService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	var out strings.Builder
	err := DeployWithClient(cfg, mockClient, &Options{Output: &out})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"MAXMIND_LICENSE_KEY": "abc123xyz",
		"BUILD_CHANNEL":       "stable",
	}, mockClient.LastBuildArgs())
	mockClient.AssertExpectations(t)
}

// Key names are useful progress output; values are never printed.
func TestDeploy_BuildArgsLogKeysNotValues(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()
	cfg.BuildArgs = map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("GetEnvFile", "myapp").Return("MAXMIND_LICENSE_KEY=abc123xyz\n", nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateManifest", 1).Return(nil)
	mockClient.On("RolloutService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	var out strings.Builder
	require.NoError(t, DeployWithClient(cfg, mockClient, &Options{Output: &out}))

	assert.Contains(t, out.String(), "MAXMIND_LICENSE_KEY")
	assert.NotContains(t, out.String(), "abc123xyz", "a resolved value must never be printed")
}

// A missing reference must stop the deploy before anything is built —
// never build with a silently empty credential.
func TestDeploy_BuildArgsMissingKeyAbortsBeforeBuild(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()
	cfg.BuildArgs = map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("GetEnvFile", "myapp").Return("SOMETHING_ELSE=1\n", nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	var out strings.Builder
	err := DeployWithClient(cfg, mockClient, &Options{Output: &out})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAXMIND_LICENSE_KEY")
	mockClient.AssertNotCalled(t, "Rsync", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "BuildImage", mock.Anything, mock.Anything)
}

// Pre-built services never build, so their env file is never read.
func TestDeploy_PrebuiltSkipsBuildArgs(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()
	cfg.Image = "nginx:latest"

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("PullImage", "nginx:latest").Return(nil)
	mockClient.On("RolloutService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	require.NoError(t, DeployWithClient(cfg, mockClient, nil))
	mockClient.AssertNotCalled(t, "GetEnvFile", mock.Anything)
}

// build_secrets resolve through the same stores as build_args, and one deploy
// must not fetch a store twice just because both maps reference it.
func TestResolveBuildInputs_SharesStoresAcrossBothMaps(t *testing.T) {
	m := new(MockDeployer)
	m.On("GetEnvFile", "api").Return("A=literal-a\nB=literal-b\n", nil)

	cfg := buildArgCfg(map[string]string{"A": "${env:A}"})
	cfg.BuildSecrets = map[string]string{"B": "${env:B}"}

	args, secrets, redact, err := resolveBuildInputs(context.Background(), m, cfg)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"A": "literal-a"}, args)
	assert.Equal(t, map[string]string{"B": "literal-b"}, secrets)
	assert.ElementsMatch(t, []string{"literal-a", "literal-b"}, redact,
		"values from both maps must be masked in build output")
	m.AssertNumberOfCalls(t, "GetEnvFile", 1)
}

func TestResolveBuildInputs_MissingBuildSecretKeyIsHardError(t *testing.T) {
	client := &secretDeployer{MockDeployer: new(MockDeployer), secrets: "OTHER=1\n"}

	cfg := buildArgCfg(nil)
	cfg.BuildSecrets = map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}

	_, _, _, err := resolveBuildInputs(context.Background(), client, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build_secrets")
	assert.Contains(t, err.Error(), "MAXMIND_LICENSE_KEY")
	assert.Contains(t, err.Error(), "api-secret")
}

func TestDeploy_BuildSecretsPassedToBuild(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()
	cfg.BuildSecrets = map[string]string{"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}"}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("GetEnvFile", "myapp").Return("MAXMIND_LICENSE_KEY=abc123xyz\n", nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateManifest", 1).Return(nil)
	mockClient.On("RolloutService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	var out strings.Builder
	require.NoError(t, DeployWithClient(cfg, mockClient, &Options{Output: &out}))

	assert.Empty(t, mockClient.LastBuildArgs(), "a build secret is not a build arg")
	assert.Equal(t, map[string]string{"MAXMIND_LICENSE_KEY": "abc123xyz"}, mockClient.LastBuildSecrets())
	assert.Contains(t, out.String(), "MAXMIND_LICENSE_KEY", "key names are logged")
	assert.NotContains(t, out.String(), "abc123xyz")
}
