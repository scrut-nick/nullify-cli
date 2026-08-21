package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/nullify-platform/cli/internal/client"
)

func resetRepoIDCache() {
	repoIDCache.Range(func(k, _ any) bool {
		repoIDCache.Delete(k)
		return true
	})
}

func TestRepositoryIDParam(t *testing.T) {
	tests := []struct {
		name        string
		queryParams map[string]string
		want        string
		wantErr     bool
	}{
		{"github", map[string]string{"githubOwnerId": "69645079"}, "githubRepositoryId", false},
		{"azure", map[string]string{"azureOrganizationId": "org"}, "azureRepositoryId", false},
		{"bitbucket", map[string]string{"bitbucketWorkspaceId": "ws"}, "bitbucketRepositoryId", false},
		{"gitlab is unsupported", map[string]string{"gitlabGroupId": "g"}, "", true},
		{"no owner configured", map[string]string{}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repositoryIDParam(tt.queryParams)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got param %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// newRepoListServer serves paged repository-stats listings shaped like the real
// ones - keyed by numeric id, PascalCase on /sca and camelCase on /sast - and
// records the paths requested against it.
func newRepoListServer(t *testing.T) (*client.NullifyClient, *[]string) {
	t.Helper()
	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/sca/repositories" && r.URL.Query().Get("nextToken") == "10":
			// A ULID-keyed entry must be skipped, not sent on as an int64.
			_, _ = w.Write([]byte(`{"repositories":{"222":{"ID":"222","Repository":"scrut-kaiService"},` +
				`"01HK153X00FYFN3CT5C25VNY1T":{"ID":"01HK153X00FYFN3CT5C25VNY1T","Repository":"scrut-ulid-only"}},` +
				`"nextToken":""}`))
		case r.URL.Path == "/sca/repositories":
			_, _ = w.Write([]byte(`{"repositories":{"111":{"ID":"111","Repository":"scrut-helpdesk-bot"}},"nextToken":"10"}`))
		default:
			// /sast/repositories, camelCase keys.
			_, _ = w.Write([]byte(`{"repositories":{"333":{"id":"333","repository":"scrut-sast-only"}},"nextToken":""}`))
		}
	}))
	t.Cleanup(srv.Close)

	return &client.NullifyClient{BaseURL: srv.URL, HttpClient: srv.Client()}, &paths
}

func TestResolveRepositoryIDPagesUntilFound(t *testing.T) {
	resetRepoIDCache()
	c, paths := newRepoListServer(t)

	// On the second page, so the resolver must follow nextToken.
	id, err := resolveRepositoryID(context.Background(), c, map[string]string{}, "scrut-kaiService")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "222" {
		t.Errorf("got id %q, want 222", id)
	}
	if len(*paths) != 2 {
		t.Errorf("expected 2 listing requests, got %d: %v", len(*paths), *paths)
	}

	// A name seen while paging is cached, so no further requests are made.
	before := len(*paths)
	id, err = resolveRepositoryID(context.Background(), c, map[string]string{}, "SCRUT-HELPDESK-BOT")
	if err != nil {
		t.Fatalf("unexpected error on cached lookup: %v", err)
	}
	if id != "111" {
		t.Errorf("got id %q, want 111", id)
	}
	if len(*paths) != before {
		t.Errorf("cached lookup issued %d extra requests", len(*paths)-before)
	}
}

func TestResolveRepositoryIDFallsThroughToSAST(t *testing.T) {
	resetRepoIDCache()
	c, _ := newRepoListServer(t)

	id, err := resolveRepositoryID(context.Background(), c, map[string]string{}, "scrut-sast-only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "333" {
		t.Errorf("got id %q, want 333", id)
	}
}

// A ULID id is rejected by the API as an int64, so it must never be resolved.
func TestResolveRepositoryIDSkipsNonNumericIDs(t *testing.T) {
	resetRepoIDCache()
	c, _ := newRepoListServer(t)

	if id, err := resolveRepositoryID(context.Background(), c, map[string]string{}, "scrut-ulid-only"); err == nil {
		t.Fatalf("expected an error for a ULID-keyed repository, got id %q", id)
	}
}

func TestResolveRepositoryIDUnknownNameErrors(t *testing.T) {
	resetRepoIDCache()
	c, _ := newRepoListServer(t)

	if _, err := resolveRepositoryID(context.Background(), c, map[string]string{}, "not-a-repo"); err == nil {
		t.Fatal("expected an error for an unknown repository, got nil")
	}
}

func TestTranslateListFiltersPushesRepositoryDown(t *testing.T) {
	resetRepoIDCache()
	c, _ := newRepoListServer(t)
	queryParams := map[string]string{"githubOwnerId": "69645079"}
	args := map[string]any{"repository": "scrut-helpdesk-bot"}

	extra, unapplied, err := translateListFilters(
		context.Background(), c, "/sca/dependencies/findings", args, queryParams)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unapplied) != 0 {
		t.Errorf("expected no unapplied filters, got %v", unapplied)
	}
	if want := []string{"githubRepositoryId", "111"}; !reflect.DeepEqual(extra, want) {
		t.Errorf("got %v, want %v", extra, want)
	}

	// The repository name itself must never reach the API: it is not a
	// parameter any endpoint understands, and unknown parameters are ignored
	// rather than rejected.
	qs := buildQueryString(queryParams, extra...)
	parsed, err := url.ParseQuery(qs[1:])
	if err != nil {
		t.Fatalf("could not parse query string %q: %v", qs, err)
	}
	if parsed.Has("repository") {
		t.Errorf("query string still carries a repository name: %q", qs)
	}
}

func TestTranslateListFiltersReportsUnsupported(t *testing.T) {
	resetRepoIDCache()
	c, _ := newRepoListServer(t)

	tests := []struct {
		name         string
		basePath     string
		args         map[string]any
		wantExtra    []string
		wantUnapplie []string
	}{
		{
			name:         "sast supports severity",
			basePath:     "/sast/findings",
			args:         map[string]any{"severity": "critical"},
			wantExtra:    []string{"severity", "critical"},
			wantUnapplie: nil,
		},
		{
			name:         "sca dependencies supports neither",
			basePath:     "/sca/dependencies/findings",
			args:         map[string]any{"severity": "critical", "status": "open"},
			wantExtra:    nil,
			wantUnapplie: []string{"severity", "status"},
		},
		{
			name:         "sast does not support status",
			basePath:     "/sast/findings",
			args:         map[string]any{"status": "open"},
			wantExtra:    nil,
			wantUnapplie: []string{"status"},
		},
		{
			name:         "cspm supports both",
			basePath:     "/cspm/findings",
			args:         map[string]any{"severity": "high", "status": "open"},
			wantExtra:    []string{"severity", "high", "status", "open"},
			wantUnapplie: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra, unapplied, err := translateListFilters(
				context.Background(), c, tt.basePath, tt.args, map[string]string{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(extra, tt.wantExtra) {
				t.Errorf("extra: got %v, want %v", extra, tt.wantExtra)
			}
			if !reflect.DeepEqual(unapplied, tt.wantUnapplie) {
				t.Errorf("unapplied: got %v, want %v", unapplied, tt.wantUnapplie)
			}
		})
	}
}

// Every endpoint the finding tools fan out over needs a support entry;
// a missing one silently reads as "supports nothing".
func TestListEndpointFiltersCoversAllFindingTypes(t *testing.T) {
	for name, cfg := range findingTypes {
		if _, ok := listEndpointFilters[cfg.basePath]; !ok {
			t.Errorf("finding type %q (%s) has no listEndpointFilters entry", name, cfg.basePath)
		}
	}
}
