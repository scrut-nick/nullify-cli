package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestTransport wires a transport around a stub round tripper with the same
// defaults the constructor uses, minus the initial token fetch.
func newTestTransport(cached string, refresh TokenProvider, rt http.RoundTripper) *refreshingAuthTransport {
	return &refreshingAuthTransport{
		nullifyHost:     "acme.nullify.ai",
		tokenProvider:   func() (string, error) { return cached, nil },
		refreshProvider: refresh,
		transport:       rt,
		cachedToken:     cached,
		cachedAt:        time.Now(),
		cacheTTL:        5 * time.Minute,
		failureBackoff:  30 * time.Second,
	}
}

// tokenRecorder serves 401 to anything but wantToken, and records every bearer
// token it was offered.
type tokenRecorder struct {
	wantToken string
	seen      []string
	bodies    []string
}

func (s *tokenRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	s.seen = append(s.seen, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))

	body := ""
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	s.bodies = append(s.bodies, body)

	code := http.StatusUnauthorized
	if s.seen[len(s.seen)-1] == s.wantToken {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestRoundTripRefreshesAndRetriesOn401(t *testing.T) {
	stub := &tokenRecorder{wantToken: "fresh-token"}
	tr := newTestTransport("stale-token", func() (string, error) { return "fresh-token", nil }, stub)

	resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.acme.nullify.ai/x", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{"stale-token", "fresh-token"}, stub.seen,
		"a 401 must trigger exactly one retry with the refreshed token")
	require.Equal(t, "fresh-token", tr.cachedToken, "the refreshed token must be cached")
}

// A token that cannot be refreshed - a flag or NULLIFY_TOKEN - must surface the
// 401 rather than replaying the same rejected string.
func TestRoundTripSurfaces401WhenSourceNotRefreshable(t *testing.T) {
	stub := &tokenRecorder{wantToken: "unreachable"}
	tr := newTestTransport("fixed-token", func() (string, error) { return "", ErrTokenNotRefreshable }, stub)

	resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.acme.nullify.ai/x", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, []string{"fixed-token"}, stub.seen, "must not retry a fixed token source")
	require.False(t, tr.refreshRetryAfter.IsZero(), "backoff must be armed so every later request does not re-refresh")
}

// Getting the same token back is not progress; retrying would loop forever.
func TestRoundTripDoesNotRetryWhenRefreshReturnsSameToken(t *testing.T) {
	stub := &tokenRecorder{wantToken: "unreachable"}
	tr := newTestTransport("same-token", func() (string, error) { return "same-token", nil }, stub)

	resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.acme.nullify.ai/x", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, []string{"same-token"}, stub.seen)
	require.Equal(t, "same-token", tr.cachedToken, "a rejected token must not overwrite the cache")
	require.False(t, tr.refreshRetryAfter.IsZero())
}

func TestRoundTripDoesNotOverwriteCacheWithEmptyToken(t *testing.T) {
	stub := &tokenRecorder{wantToken: "unreachable"}
	tr := newTestTransport("good-token", func() (string, error) { return "", nil }, stub)

	_, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.acme.nullify.ai/x", nil))
	require.NoError(t, err)
	require.Equal(t, "good-token", tr.cachedToken, "an empty refresh must leave the working token in place")
}

// The retry must send the same body, not an empty one.
func TestRoundTripReplaysBodyOnRetry(t *testing.T) {
	stub := &tokenRecorder{wantToken: "fresh-token"}
	tr := newTestTransport("stale-token", func() (string, error) { return "fresh-token", nil }, stub)

	req := httptest.NewRequest(http.MethodPost, "https://api.acme.nullify.ai/x", strings.NewReader(`{"a":1}`))
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{`{"a":1}`, `{"a":1}`}, stub.bodies, "the replayed request must carry the original body")
}

// While backoff is armed, a 401 must not call the refresh provider again.
func TestForceRefreshHonoursBackoffWindow(t *testing.T) {
	var calls atomic.Int32
	stub := &tokenRecorder{wantToken: "unreachable"}
	tr := newTestTransport("stale-token", func() (string, error) {
		calls.Add(1)
		return "", ErrTokenNotRefreshable
	}, stub)

	for i := 0; i < 3; i++ {
		_, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.acme.nullify.ai/x", nil))
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), calls.Load(), "backoff must suppress repeated refresh attempts")
}

// Another in-flight request may have already replaced the token.
func TestForceRefreshReturnsCurrentTokenWhenAlreadyRotated(t *testing.T) {
	tr := newTestTransport("newer-token", func() (string, error) {
		t.Fatal("refreshProvider must not be called when the cache already moved on")
		return "", nil
	}, &tokenRecorder{})

	require.Equal(t, "newer-token", tr.forceRefresh(context.Background(), "older-token"))
}

func TestRoundTripLeavesNon401ResponsesAlone(t *testing.T) {
	stub := &tokenRecorder{wantToken: "cached-token"}
	tr := newTestTransport("cached-token", func() (string, error) {
		t.Fatal("a successful response must not trigger a refresh")
		return "", nil
	}, stub)

	resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.acme.nullify.ai/x", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stub.seen, 1)
}

// A failing tokenProvider must serve the cached token and arm its own clock,
// separate from the refresh clock.
func TestGetTokenBacksOffAfterProviderFailure(t *testing.T) {
	var calls atomic.Int32
	tr := newTestTransport("cached-token", nil, &tokenRecorder{})
	tr.tokenProvider = func() (string, error) {
		calls.Add(1)
		return "", io.ErrUnexpectedEOF
	}
	tr.cachedAt = time.Now().Add(-time.Hour) // force the TTL path

	require.Equal(t, "cached-token", tr.getToken(context.Background()))
	require.Equal(t, "cached-token", tr.getToken(context.Background()))
	require.Equal(t, int32(1), calls.Load(), "provider backoff must suppress the second attempt")
	require.True(t, tr.refreshRetryAfter.IsZero(), "a provider failure must not arm the refresh clock")
}
