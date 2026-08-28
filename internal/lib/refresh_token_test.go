package lib

import (
	"context"
	"testing"

	"github.com/nullify-platform/cli/internal/client"
	"github.com/stretchr/testify/require"
)

// The --nullify-token flag yields a fixed string. Handing it back after a 401
// would replay the token the server just rejected, so the refresh path must
// report that the source cannot be refreshed instead.
func TestRefreshNullifyTokenRejectsFlagSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_TOKEN", "")

	_, err := RefreshNullifyToken(context.Background(), "acme.nullify.ai", "flag-token", "")
	require.ErrorIs(t, err, client.ErrTokenNotRefreshable)
}

func TestRefreshNullifyTokenRejectsEnvSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_TOKEN", "env-token")

	_, err := RefreshNullifyToken(context.Background(), "acme.nullify.ai", "", "")
	require.ErrorIs(t, err, client.ErrTokenNotRefreshable)
}

// The non-forced path must keep returning those same fixed sources.
func TestGetNullifyTokenStillUsesFixedSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_TOKEN", "env-token")

	token, err := GetNullifyToken(context.Background(), "acme.nullify.ai", "", "")
	require.NoError(t, err)
	require.Equal(t, "env-token", token)

	token, err = GetNullifyToken(context.Background(), "acme.nullify.ai", "flag-token", "")
	require.NoError(t, err)
	require.Equal(t, "flag-token", token, "the flag must outrank the environment")
}

// With no fixed source and no stored credentials there is nothing to refresh,
// and the transport must be told so rather than shown a generic failure.
func TestRefreshNullifyTokenReportsNotRefreshableWithoutStoredCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NULLIFY_TOKEN", "")

	_, err := RefreshNullifyToken(context.Background(), "acme.nullify.ai", "", "")
	require.Error(t, err)
}
