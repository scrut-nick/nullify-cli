package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Credentials with no refresh token can never yield a new access token, so the
// caller must be able to distinguish that from a transient failure and stop
// retrying.
func TestForceRefreshTokenReportsNotRefreshableWithoutRefreshToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, SaveHostCredentials("acme.nullify.ai", HostCredentials{
		AccessToken: "access-only",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}))

	_, err := ForceRefreshToken(context.Background(), "acme.nullify.ai")
	require.ErrorIs(t, err, ErrNotRefreshable)
}

func TestForceRefreshTokenRequiresCredentialsForHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, SaveHostCredentials("other.nullify.ai", HostCredentials{
		AccessToken:  "a",
		RefreshToken: "r",
	}))

	_, err := ForceRefreshToken(context.Background(), "acme.nullify.ai")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotRefreshable,
		"a missing host is not the same as a host with no refresh token")
}

// ForceRefreshToken must attempt a refresh even while the stored access token
// still looks valid - that is the whole point of it versus GetValidToken, which
// would hand back the unexpired-but-revoked token unchanged.
func TestForceRefreshTokenIgnoresUnexpiredAccessToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, SaveHostCredentials("acme.nullify.ai", HostCredentials{
		AccessToken:  "still-valid-looking",
		RefreshToken: "refresh-me",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}))

	// GetValidToken short-circuits and returns the stored token untouched.
	stored, err := GetValidToken(context.Background(), "acme.nullify.ai")
	require.NoError(t, err)
	require.Equal(t, "still-valid-looking", stored)

	// ForceRefreshToken instead reaches the network; with no server reachable
	// it fails, which still proves it did not short-circuit on the expiry.
	got, err := ForceRefreshToken(context.Background(), "acme.nullify.ai")
	require.Error(t, err)
	require.NotEqual(t, "still-valid-looking", got)
}
