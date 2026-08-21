package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/nullify-platform/cli/internal/client"

	"github.com/mark3labs/mcp-go/mcp"
)

// The Nullify API ignores query parameters it does not recognise instead of
// rejecting them, so a filter forwarded to an endpoint that does not support it
// comes back as a full, unfiltered page that reads as a filtered one. Every
// filter the MCP tools expose is therefore either translated into a parameter
// the endpoint actually accepts or reported as unapplied - never sent and hoped
// for.

// listFilterSupport records which of the tool-level filters an endpoint honours
// server-side. Derived from the query parameters each generated client method
// forwards in internal/api.
type listFilterSupport struct {
	severity bool
	status   bool
}

var listEndpointFilters = map[string]listFilterSupport{
	"/sast/findings":             {severity: true},
	"/sca/dependencies/findings": {},
	"/sca/containers/findings":   {},
	"/secrets/findings":          {},
	"/dast/pentest/findings":     {},
	"/dast/bughunt/findings":     {},
	"/cspm/findings":             {severity: true, status: true},
}

// translatedFilters are the tool-level arguments handled by
// translateListFilters. They must not also be forwarded verbatim.
var translatedFilters = map[string]bool{
	"repository": true,
	"severity":   true,
	"status":     true,
}

// repositoryIDParams maps the owner parameter identifying the VCS provider to
// the repository-id parameter its endpoints filter on. No endpoint accepts a
// repository name.
var repositoryIDParams = []struct{ ownerParam, repoParam string }{
	{"githubOwnerId", "githubRepositoryId"},
	{"azureOrganizationId", "azureRepositoryId"},
	{"bitbucketWorkspaceId", "bitbucketRepositoryId"},
}

// repoIDCache memoises repository name -> id per API host for the life of the
// process. Repository ids are stable, and the listing is paged, so resolving
// one name can otherwise cost several requests per tool call.
var repoIDCache sync.Map

func repoCacheKey(baseURL, name string) string {
	return baseURL + "\x00" + strings.ToLower(name)
}

// repositoryIDParam returns the repository-id parameter for the configured VCS
// provider.
func repositoryIDParam(queryParams map[string]string) (string, error) {
	for _, p := range repositoryIDParams {
		if queryParams[p.ownerParam] != "" {
			return p.repoParam, nil
		}
	}
	if queryParams["gitlabGroupId"] != "" {
		return "", fmt.Errorf("filtering by repository is not supported for GitLab: the API exposes no GitLab repository-id parameter")
	}
	return "", fmt.Errorf("cannot filter by repository: no VCS owner is configured in the stored credentials")
}

// repositoryStatsEndpoints are the listings that expose the numeric VCS
// repository id the findings endpoints filter on.
//
// /context/repositories is deliberately not used here: its "id" is a
// Nullify-internal ULID, and passing one as githubRepositoryId is rejected with
// HTTP 400 ("invalid integer value ... type 'int64'"). These stats listings are
// keyed by the numeric id instead, which is the same value findings carry as
// repositoryId.
var repositoryStatsEndpoints = []string{"/sca/repositories", "/sast/repositories"}

// resolveRepositoryID maps a repository name to the numeric id the findings
// endpoints filter on, paging the repository stats listings until the name
// matches or they are exhausted.
//
// The listings cap limit at 10, so a miss costs one request per ten
// repositories. Every name seen on the way is cached, so the cost is paid at
// most once per process for any repository that appears before the target.
func resolveRepositoryID(ctx context.Context, c *client.NullifyClient, queryParams map[string]string, name string) (string, error) {
	if id, ok := repoIDCache.Load(repoCacheKey(c.BaseURL, name)); ok {
		return id.(string), nil
	}

	seen := 0
	for _, endpoint := range repositoryStatsEndpoints {
		// Bounded so a listing that keeps handing back a nextToken cannot spin.
		const maxPages = 200
		nextToken := ""

		for page := 0; page < maxPages; page++ {
			extra := []string{"limit", "10"}
			if nextToken != "" {
				extra = append(extra, "nextToken", nextToken)
			}

			result, err := doGet(ctx, c, endpoint+buildQueryString(queryParams, extra...))
			if err != nil {
				return "", err
			}
			body, err := resultText(result)
			if err != nil {
				return "", fmt.Errorf("could not list repositories from %s: %w", endpoint, err)
			}

			// Keys differ in case between the two listings (Repository vs
			// repository); encoding/json matches field names case-insensitively,
			// so one set of tags covers both.
			var payload struct {
				Repositories map[string]struct {
					Repository string `json:"repository"`
				} `json:"repositories"`
				NextToken string `json:"nextToken"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				return "", fmt.Errorf("could not parse the repository listing from %s: %w", endpoint, err)
			}

			for id, repo := range payload.Repositories {
				// The API types this parameter as int64 and rejects anything
				// else, so skip a key that is not numeric rather than provoke a
				// 400 later.
				if repo.Repository == "" || !isNumericID(id) {
					continue
				}
				repoIDCache.Store(repoCacheKey(c.BaseURL, repo.Repository), id)
				seen++
				if strings.EqualFold(repo.Repository, name) {
					return id, nil
				}
			}

			if payload.NextToken == "" || len(payload.Repositories) == 0 {
				break
			}
			nextToken = payload.NextToken
		}
	}

	return "", fmt.Errorf("repository %q is not among the %d scanned repositories on this Nullify instance", name, seen)
}

func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// translateListFilters converts tool-level filter arguments for one list
// endpoint into query parameters that endpoint honours. Filters it cannot apply
// are returned in unapplied instead of being sent.
func translateListFilters(
	ctx context.Context,
	c *client.NullifyClient,
	basePath string,
	args map[string]any,
	queryParams map[string]string,
) (extra []string, unapplied []string, err error) {
	support := listEndpointFilters[basePath]

	if repository := getStringArg(args, "repository"); repository != "" {
		param, err := repositoryIDParam(queryParams)
		if err != nil {
			return nil, nil, err
		}
		id, err := resolveRepositoryID(ctx, c, queryParams, repository)
		if err != nil {
			return nil, nil, err
		}
		extra = append(extra, param, id)
	}

	if severity := getStringArg(args, "severity"); severity != "" {
		if support.severity {
			extra = append(extra, "severity", severity)
		} else {
			unapplied = append(unapplied, "severity")
		}
	}

	if status := getStringArg(args, "status"); status != "" {
		if support.status {
			extra = append(extra, "status", status)
		} else {
			unapplied = append(unapplied, "status")
		}
	}

	return extra, unapplied, nil
}

// unsupportedFilterError describes filters an endpoint cannot apply, for tools
// that surface a single endpoint and so must fail rather than return results
// the caller would read as filtered.
func unsupportedFilterError(basePath string, unapplied []string) error {
	return fmt.Errorf(
		"%s cannot filter by %s; omit the filter and narrow the results yourself",
		basePath,
		strings.Join(unapplied, " or "),
	)
}

// resultText extracts the text payload of a tool result, turning an error
// result back into an error.
func resultText(result *mcp.CallToolResult) (string, error) {
	if result == nil || len(result.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return "", fmt.Errorf("unexpected response content type %T", result.Content[0])
	}
	if result.IsError {
		return "", fmt.Errorf("%s", tc.Text)
	}
	return tc.Text, nil
}
