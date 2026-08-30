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

// rewriteTransport sends requests aimed at the real API to a test server
// instead, so refreshToken can be exercised end to end without reaching the
// network or making its URL construction configurable.
type rewriteTransport struct{ base *url.URL }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.base.Scheme
	clone.URL.Host = rt.base.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// serveRefresh points the package HTTP client at handler for the duration of
// the test and returns the refresh_token cookie each request carried.
func serveRefresh(t *testing.T, handler http.HandlerFunc) *string {
	t.Helper()

	seen := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("refresh_token"); err == nil {
			*seen = c.Value
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	base, err := url.Parse(srv.URL)
	require.NoError(t, err)

	prev := httpClient
	httpClient = &http.Client{Transport: rewriteTransport{base: base}, Timeout: 5 * time.Second}
	t.Cleanup(func() { httpClient = prev })

	return seen
}

func storedRefreshToken(t *testing.T, host string) string {
	t.Helper()
	creds, err := LoadCredentials()
	require.NoError(t, err)
	return creds[CredentialKey(host)].RefreshToken
}

// The documented upstream contract: the endpoint does not rotate the refresh
// token, so the stored one must survive a refresh untouched.
func TestRefreshTokenKeepsStoredTokenWhenServerDoesNotRotate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	sent := serveRefresh(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "access_token", Value: "fresh-access", MaxAge: 3600})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	got, err := refreshToken(context.Background(), "acme.nullify.ai", "original-refresh")
	require.NoError(t, err)
	require.Equal(t, "fresh-access", got)
	require.Equal(t, "original-refresh", *sent, "the stored refresh token must be presented as a cookie")
	require.Equal(t, "original-refresh", storedRefreshToken(t, "acme.nullify.ai"))
}

// If the deployment ever does rotate, the replacement arrives as a Set-Cookie
// and must be persisted. Re-storing the old value writes back a token the
// server has already invalidated - the next container seeded from the secret
// store then gets "invalid refresh token" with no local evidence of why.
func TestRefreshTokenPersistsRotatedTokenFromCookie(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	serveRefresh(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "access_token", Value: "fresh-access", MaxAge: 3600})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "rotated-refresh", MaxAge: 2592000})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	got, err := refreshToken(context.Background(), "acme.nullify.ai", "original-refresh")
	require.NoError(t, err)
	require.Equal(t, "fresh-access", got)
	require.Equal(t, "rotated-refresh", storedRefreshToken(t, "acme.nullify.ai"))
}

// A rotated token delivered in the JSON body must be persisted too; the body
// and the cookie are the same signal arriving by different routes.
func TestRefreshTokenPersistsRotatedTokenFromBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	serveRefresh(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"body-access","refreshToken":"body-rotated","expiresIn":3600}`))
	})

	got, err := refreshToken(context.Background(), "acme.nullify.ai", "original-refresh")
	require.NoError(t, err)
	require.Equal(t, "body-access", got)
	require.Equal(t, "body-rotated", storedRefreshToken(t, "acme.nullify.ai"))
}

// The refresh cookie must still be read when the body already carried an
// access token, otherwise rotation is silently dropped on that path.
func TestRefreshTokenReadsRotatedCookieAlongsideBodyAccessToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	serveRefresh(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "cookie-rotated", MaxAge: 2592000})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"body-access","expiresIn":3600}`))
	})

	got, err := refreshToken(context.Background(), "acme.nullify.ai", "original-refresh")
	require.NoError(t, err)
	require.Equal(t, "body-access", got)
	require.Equal(t, "cookie-rotated", storedRefreshToken(t, "acme.nullify.ai"))
}

// A refresh that fails must not overwrite good stored credentials.
func TestRefreshTokenLeavesCredentialsIntactOnRejection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, SaveHostCredentials("acme.nullify.ai", HostCredentials{
		AccessToken:  "old-access",
		RefreshToken: "original-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}))

	serveRefresh(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"UNAUTHENTICATED","error":"unauthenticated: invalid refresh token"}`))
	})

	_, err := refreshToken(context.Background(), "acme.nullify.ai", "original-refresh")
	require.Error(t, err)
	require.Equal(t, "original-refresh", storedRefreshToken(t, "acme.nullify.ai"))
}
