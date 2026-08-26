package cmd

import (
	"context"
	"testing"

	"github.com/nullify-platform/cli/internal/auth"
	"github.com/stretchr/testify/require"
)

// resolveHostForAuth backs 'auth token' and 'auth status'. It must resolve the
// host the same way every other command does (flag, then NULLIFY_HOST, then
// config file) — otherwise those two commands are unusable anywhere the host
// comes from the environment rather than a config file, which is how the MCP
// server runs in ephemeral containers.

func withoutHostFlag(t *testing.T, value string) {
	t.Helper()
	original := host
	host = value
	t.Cleanup(func() { host = original })
}

func TestResolveHostForAuthUsesEnvWithoutConfigFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "api.acme.nullify.ai")
	withoutHostFlag(t, "")

	require.Equal(t, "acme.nullify.ai", resolveHostForAuth(context.Background()))
}

func TestResolveHostForAuthPrefersFlagOverEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "env.nullify.ai")
	withoutHostFlag(t, "flag.nullify.ai")

	require.Equal(t, "flag.nullify.ai", resolveHostForAuth(context.Background()))
}

func TestResolveHostForAuthPrefersEnvOverConfigFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, auth.SaveConfig(&auth.Config{Host: "config.nullify.ai"}))
	t.Setenv("NULLIFY_HOST", "env.nullify.ai")
	withoutHostFlag(t, "")

	require.Equal(t, "env.nullify.ai", resolveHostForAuth(context.Background()))
}

func TestResolveHostForAuthFallsBackToConfigWhenEnvInvalid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, auth.SaveConfig(&auth.Config{Host: "config.nullify.ai"}))
	t.Setenv("NULLIFY_HOST", "example.com")
	withoutHostFlag(t, "")

	require.Equal(t, "config.nullify.ai", resolveHostForAuth(context.Background()))
}
