package cmd

import (
	"context"
	"testing"

	"github.com/nullify-platform/cli/internal/auth"
	"github.com/stretchr/testify/require"
)

// withCleanHostFlag isolates the package-level --host flag so each case starts
// from a known state and later tests are unaffected.
func withCleanHostFlag(t *testing.T, value string) {
	t.Helper()

	original := host
	host = value
	t.Cleanup(func() { host = original })
}

func TestLookupHostForAuthUsesEnvWhenNoFlagOrConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "acme.nullify.ai")
	withCleanHostFlag(t, "")

	resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
	require.True(t, ok, "NULLIFY_HOST should satisfy auth host resolution")
	require.Equal(t, "acme.nullify.ai", resolved)
}

func TestLookupHostForAuthPrefersFlagOverEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "from-env.nullify.ai")
	withCleanHostFlag(t, "from-flag.nullify.ai")

	resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
	require.True(t, ok)
	require.Equal(t, "from-flag.nullify.ai", resolved)
}

func TestLookupHostForAuthPrefersEnvOverConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "from-env.nullify.ai")
	withCleanHostFlag(t, "")

	require.NoError(t, auth.SaveConfig(&auth.Config{Host: "from-config.nullify.ai"}))

	resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
	require.True(t, ok)
	require.Equal(t, "from-env.nullify.ai", resolved)
}

func TestLookupHostForAuthFallsBackToConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "")
	withCleanHostFlag(t, "")

	require.NoError(t, auth.SaveConfig(&auth.Config{Host: "from-config.nullify.ai"}))

	resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
	require.True(t, ok)
	require.Equal(t, "from-config.nullify.ai", resolved)
}

// An unusable NULLIFY_HOST must not shadow a working config file, mirroring
// resolveHost's behaviour for the non-auth commands.
func TestLookupHostForAuthIgnoresInvalidEnvAndUsesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "not a valid host!!")
	withCleanHostFlag(t, "")

	require.NoError(t, auth.SaveConfig(&auth.Config{Host: "from-config.nullify.ai"}))

	resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
	require.True(t, ok)
	require.Equal(t, "from-config.nullify.ai", resolved)
}

func TestLookupHostForAuthReportsAbsenceInsteadOfExiting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_HOST", "")
	withCleanHostFlag(t, "")

	resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
	require.False(t, ok, "no host anywhere should report absence, not exit")
	require.Empty(t, resolved)
}

// The bare and api-prefixed spellings must both normalise to the credential
// key form, so a seeded credentials file is found either way.
func TestLookupHostForAuthNormalisesEnvHostSpellings(t *testing.T) {
	for _, envHost := range []string{
		"acme",
		"acme.nullify.ai",
		"api.acme.nullify.ai",
		"https://acme.nullify.ai/",
	} {
		t.Run(envHost, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("NULLIFY_HOST", envHost)
			withCleanHostFlag(t, "")

			resolved, ok := lookupHostForAuth(setupLogger(context.Background()))
			require.True(t, ok)
			require.Equal(t, "acme.nullify.ai", resolved)
		})
	}
}
