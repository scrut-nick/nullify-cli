package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The refresh endpoint returns tokens as Set-Cookie headers. If the backend
// rotates the refresh token and the CLI keeps storing the old one, the
// credential dies at the *next* refresh — the failure surfaces an hour later
// as "refresh failed with status 401", far from its cause. These tests pin
// down what gets stored in each case.

// hostRewrite points the package's http client at a test server while leaving
// the request path (and cookies) exactly as refreshToken built them.
type hostRewrite struct{ target *url.URL }

func (h hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = h.target.Scheme
	clone.URL.Host = h.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func serveRefresh(t *testing.T, handler http.HandlerFunc) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	require.NoError(t, err)

	original := httpClient.Transport
	httpClient.Transport = hostRewrite{target: target}
	t.Cleanup(func() { httpClient.Transport = original })
}

func storedCredentials(t *testing.T, host string) HostCredentials {
	t.Helper()

	creds, err := LoadCredentials()
	require.NoError(t, err)

	hostCreds, ok := creds[CredentialKey(host)]
	require.True(t, ok, "expected credentials for %s", host)
	return hostCreds
}

func seedExpiredCredentials(t *testing.T, host string) {
	t.Helper()

	require.NoError(t, SaveHostCredentials(host, HostCredentials{
		AccessToken:  "stale-access",
		RefreshToken: "original-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}))
}

func TestRefreshTokenStoresRotatedRefreshTokenFromCookie(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const host = "acme.nullify.ai"
	seedExpiredCredentials(t, host)

	var sentRefreshToken string
	serveRefresh(t, func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("refresh_token"); err == nil {
			sentRefreshToken = cookie.Value
		}
		http.SetCookie(w, &http.Cookie{Name: "access_token", Value: "new-access", MaxAge: 3600})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "rotated-refresh"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	token, err := refreshToken(context.Background(), host, "original-refresh")
	require.NoError(t, err)
	require.Equal(t, "new-access", token)
	require.Equal(t, "original-refresh", sentRefreshToken, "old refresh token should be sent as a cookie")

	saved := storedCredentials(t, host)
	require.Equal(t, "rotated-refresh", saved.RefreshToken, "rotated refresh token must replace the old one")
	require.Equal(t, "new-access", saved.AccessToken)
	require.Greater(t, saved.ExpiresAt, time.Now().Unix())
}

func TestRefreshTokenKeepsRefreshTokenWhenBackendDoesNotRotate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const host = "acme.nullify.ai"
	seedExpiredCredentials(t, host)

	serveRefresh(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "access_token", Value: "new-access", MaxAge: 3600})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	_, err := refreshToken(context.Background(), host, "original-refresh")
	require.NoError(t, err)

	require.Equal(t, "original-refresh", storedCredentials(t, host).RefreshToken)
}

func TestRefreshTokenPrefersRotatedRefreshTokenFromBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const host = "acme.nullify.ai"
	seedExpiredCredentials(t, host)

	serveRefresh(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "cookie-refresh"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"body-access","refreshToken":"body-refresh","expiresIn":3600}`))
	})

	token, err := refreshToken(context.Background(), host, "original-refresh")
	require.NoError(t, err)
	require.Equal(t, "body-access", token)

	require.Equal(t, "body-refresh", storedCredentials(t, host).RefreshToken)
}

func TestRefreshTokenIgnoresEmptyRotatedRefreshToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const host = "acme.nullify.ai"
	seedExpiredCredentials(t, host)

	// A cookie clearing the refresh token must not wipe the stored one.
	serveRefresh(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "access_token", Value: "new-access", MaxAge: 3600})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "", MaxAge: -1})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	_, err := refreshToken(context.Background(), host, "original-refresh")
	require.NoError(t, err)

	require.Equal(t, "original-refresh", storedCredentials(t, host).RefreshToken)
}
