package deploy

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/byteink/ssd/config"
)

// secretStore is the optional capability the k3s client provides: reading the
// {service}-secret Secret. Compose has no equivalent store, so `${secret:KEY}`
// there resolves from the service env file instead — one ssd.yaml keeps
// working across both runtimes.
type secretStore interface {
	ListSecrets(ctx context.Context, serviceName string) (string, error)
}

// resolveBuildInputs turns the configured build_args and build_secrets into
// the literal values handed to the builder, resolving `${secret:KEY}` /
// `${env:KEY}` against what is already stored on the server for this service.
//
// Both maps hold the same value shapes and share the same stores (each is
// fetched at most once per deploy); they differ only in transport —
// build_args become `--build-arg`, build_secrets become BuildKit `--secret`
// mounts that never enter an image layer or the image history.
//
// redact collects every value that came out of a store. Those are credentials
// and must be kept out of the build output (see redactWriter). A reference
// that names a missing or empty key is a hard error: building with a silently
// empty credential produces a broken image that looks like a successful
// deploy.
//
// No error returned here contains a resolved value.
func resolveBuildInputs(ctx context.Context, client Deployer, cfg *config.Config) (args, secrets map[string]string, redact []string, err error) {
	if len(cfg.BuildArgs) == 0 && len(cfg.BuildSecrets) == 0 {
		return nil, nil, nil, nil
	}

	stores := newBuildArgStores(client, cfg.Name)

	args, argRedact, err := resolveEntries(ctx, stores, "build_args", cfg.BuildArgs)
	if err != nil {
		return nil, nil, nil, err
	}
	secrets, secretRedact, err := resolveEntries(ctx, stores, "build_secrets", cfg.BuildSecrets)
	if err != nil {
		return nil, nil, nil, err
	}
	return args, secrets, append(argRedact, secretRedact...), nil
}

// resolveEntries resolves one map. field names it in error messages.
func resolveEntries(ctx context.Context, stores *buildArgStores, field string, entries map[string]string) (map[string]string, []string, error) {
	if len(entries) == 0 {
		return nil, nil, nil
	}
	values := make(map[string]string, len(entries))
	var redact []string
	for _, key := range buildArgKeys(entries) {
		raw := entries[key]
		kind, refKey, isRef := config.ParseBuildArgRef(raw)
		if !isRef {
			values[key] = raw
			continue
		}
		value, err := stores.lookup(ctx, field, key, kind, refKey)
		if err != nil {
			return nil, nil, err
		}
		values[key] = value
		redact = append(redact, value)
	}
	return values, redact, nil
}

// buildArgStores fetches each backing store at most once per deploy.
type buildArgStores struct {
	client  Deployer
	service string

	env    map[string]string
	secret map[string]string
}

func newBuildArgStores(client Deployer, service string) *buildArgStores {
	return &buildArgStores{client: client, service: service}
}

// lookup resolves one reference, loading its store on first use.
func (s *buildArgStores) lookup(ctx context.Context, field, argKey, kind, refKey string) (string, error) {
	store, source, err := s.storeFor(ctx, kind)
	if err != nil {
		return "", err
	}
	value, ok := store[refKey]
	if !ok {
		return "", fmt.Errorf("%s %q: %s not found in %s", field, argKey, refKey, source)
	}
	if value == "" {
		return "", fmt.Errorf("%s %q: %s is empty in %s", field, argKey, refKey, source)
	}
	return value, nil
}

// storeFor returns the map for a reference kind plus a human description of
// where it was read from, for the error message.
func (s *buildArgStores) storeFor(ctx context.Context, kind string) (map[string]string, string, error) {
	secretClient, hasSecrets := s.client.(secretStore)
	if kind == "secret" && hasSecrets {
		if s.secret == nil {
			content, err := secretClient.ListSecrets(ctx, s.service)
			if err != nil {
				return nil, "", fmt.Errorf("failed to read secret %s: %w", s.service+"-secret", err)
			}
			s.secret = parseKVLines(content)
		}
		return s.secret, "secret " + s.service + "-secret", nil
	}

	if s.env == nil {
		content, err := s.client.GetEnvFile(ctx, s.service)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read %s.env on the server: %w", s.service, err)
		}
		s.env = parseKVLines(content)
	}
	source := s.service + ".env on the server"
	if kind == "secret" {
		source += " (this runtime has no secret store, so ${secret:} reads the env file)"
	}
	return s.env, source, nil
}

// parseKVLines parses KEY=VALUE lines — the shape of both a .env file and the
// `ssd secret list` output. Blank lines, comments and lines without a `=` are
// skipped. Only the key is trimmed: a stored value keeps its exact bytes.
func parseKVLines(content string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

// buildArgKeys returns the keys in sorted order, so the generated build
// command and the logged key list are identical across deploys.
func buildArgKeys(args map[string]string) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
