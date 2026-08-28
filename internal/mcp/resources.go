package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nullify-platform/cli/internal/client"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerResources(s *server.MCPServer, c *client.NullifyClient, queryParams map[string]string) {
	s.AddResource(
		mcplib.Resource{
			URI:         "nullify://posture",
			Name:        "Security Posture",
			Description: "Current security posture summary across all finding types",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, request mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
			qs := buildQueryString(queryParams)
			result, err := doPost(ctx, c, "/admin/metrics/overview"+qs, metricsOverviewBody())
			if err != nil {
				return nil, err
			}

			var text string
			if len(result.Content) > 0 {
				if tc, ok := result.Content[0].(mcplib.TextContent); ok {
					text = tc.Text
				}
			}

			return []mcplib.ResourceContents{
				mcplib.TextResourceContents{
					URI:      "nullify://posture",
					MIMEType: "application/json",
					Text:     text,
				},
			}, nil
		},
	)

	s.AddResource(
		mcplib.Resource{
			URI:         "nullify://repos",
			Name:        "Monitored Repositories",
			Description: "List of repositories monitored by Nullify",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, request mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
			qs := buildQueryString(queryParams)
			result, err := doGet(ctx, c, "/context/repositories"+qs)
			if err != nil {
				return nil, err
			}

			var text string
			if len(result.Content) > 0 {
				if tc, ok := result.Content[0].(mcplib.TextContent); ok {
					text = tc.Text
				}
			}

			return []mcplib.ResourceContents{
				mcplib.TextResourceContents{
					URI:      "nullify://repos",
					MIMEType: "application/json",
					Text:     text,
				},
			}, nil
		},
	)

	s.AddResource(
		mcplib.Resource{
			URI:         "nullify://recent-findings",
			Name:        "Recent Findings",
			Description: "Recent findings across the top scanner types (SAST, SCA dependencies, secrets)",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, request mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
			type recentResult struct {
				Type  string          `json:"type"`
				Error string          `json:"error,omitempty"`
				Data  json.RawMessage `json:"data,omitempty"`
			}

			endpoints := []struct {
				name string
				path string
			}{
				{"sast", "/sast/findings"},
				{"sca_dependencies", "/sca/dependencies/findings"},
				{"secrets", "/secrets/findings"},
			}

			var results []recentResult
			epQS := buildQueryString(queryParams, "limit", "5")
			for _, ep := range endpoints {
				result, err := doGet(ctx, c, ep.path+epQS)
				if err != nil {
					results = append(results, recentResult{Type: ep.name, Error: err.Error()})
					continue
				}
				data, errText := aggregatePayload(result)
				results = append(results, recentResult{Type: ep.name, Data: data, Error: errText})
			}

			out, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("encoding recent findings: %w", err)
			}
			return []mcplib.ResourceContents{
				mcplib.TextResourceContents{
					URI:      "nullify://recent-findings",
					MIMEType: "application/json",
					Text:     string(out),
				},
			}, nil
		},
	)

	s.AddResource(
		mcplib.Resource{
			URI:         "nullify://config",
			Name:        "Global Configuration",
			Description: "Nullify global configuration settings",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, request mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
			qs := buildQueryString(queryParams)
			result, err := doGet(ctx, c, "/admin/globalConfig"+qs)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch config: %w", err)
			}

			return []mcplib.ResourceContents{
				mcplib.TextResourceContents{
					URI:      "nullify://config",
					MIMEType: "application/json",
					Text:     resultText(result),
				},
			}, nil
		},
	)
}
