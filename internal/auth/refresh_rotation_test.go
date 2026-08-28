package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// refreshAgainst points the refresh flow at a test server. The production code
// builds "https://api.<host>/auth/refresh_token", so the host is derived from
// the test server's address and the transport is redirected to it.
func refreshAgainst(t *testing.T, handler http.HandlerFunc) func(refreshTok string) (string, error) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	target := strings.TrimPrefix(srv.URL, "http://")

	originalClient := httpClient
	httpClient = &http.Client{
		Timeout:   10 * time.Second,
		Transport: rewriteTransport{target: target},
	}
	t.Cleanup(func() { httpClient = originalClient })

	return func(refreshTok string) (string, error) {
		return refreshToken(context.Background(), "acme.nullify.ai", refreshTok)
	}
}

// rewriteTransport sends every request to the test server over plain HTTP,
// preserving the path so the handler still sees /auth/refresh_token.
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = rt.target
	return http.DefaultTransport.RoundTrip(req)
}

func storedRefreshToken(t *testing.T) string {
	t.Helper()

	creds, err := LoadCredentials()
	require.NoError(t, err)
	return creds[CredentialKey("acme.nullify.ai")].RefreshToken
}

func TestRefreshPersistsRotatedTokenFromJSONBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := refreshAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"new-access","refreshToken":"rotated-abc","expiresIn":3600}`))
	})

	token, err := refresh("original-xyz")
	require.NoError(t, err)
	require.Equal(t, "new-access", token)
	require.Equal(t, "rotated-abc", storedRefreshToken(t),
		"a rotated refresh token must replace the one we sent")
}

func TestRefreshPersistsRotatedTokenFromSnakeCaseBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := refreshAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"new-access","refresh_token":"rotated-snake","expiresIn":3600}`))
	})

	_, err := refresh("original-xyz")
	require.NoError(t, err)
	require.Equal(t, "rotated-snake", storedRefreshToken(t))
}

// The access token already arrives via Set-Cookie on this endpoint, so a
// rotated refresh token plausibly arrives the same way.
func TestRefreshPersistsRotatedTokenFromCookie(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := refreshAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "access_token", Value: "cookie-access", MaxAge: 3600})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "rotated-cookie", MaxAge: 86400})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	token, err := refresh("original-xyz")
	require.NoError(t, err)
	require.Equal(t, "cookie-access", token)
	require.Equal(t, "rotated-cookie", storedRefreshToken(t))
}

// Non-rotating providers send nothing back; the existing token must survive.
func TestRefreshKeepsExistingTokenWhenServerDoesNotRotate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := refreshAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"new-access","expiresIn":3600}`))
	})

	_, err := refresh("original-xyz")
	require.NoError(t, err)
	require.Equal(t, "original-xyz", storedRefreshToken(t),
		"absent rotation must carry the existing refresh token forward")
}

func TestRefreshSurfacesNonOKStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := refreshAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := refresh("expired-token")
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}
