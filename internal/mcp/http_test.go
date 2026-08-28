package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestAggregatePayloadClassifiesResults(t *testing.T) {
	tests := []struct {
		name     string
		result   *mcp.CallToolResult
		wantData string
		wantErr  string
	}{
		{
			name:     "valid JSON passes through as data",
			result:   toolResult(`{"numItems":1}`),
			wantData: `{"numItems":1}`,
		},
		{
			name:    "API error becomes an error string, not data",
			result:  toolError(fmt.Errorf(`API returned 500: {"message":"Internal Server Error"}`)),
			wantErr: "API returned 500",
		},
		{
			name:    "non-JSON success body is reported, not embedded",
			result:  toolResult("<html>gateway timeout</html>"),
			wantErr: "not valid JSON",
		},
		{
			name:    "empty result is named rather than silently dropped",
			result:  toolResult(""),
			wantErr: "no content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, errText := aggregatePayload(tt.result)

			if tt.wantData != "" {
				if string(data) != tt.wantData {
					t.Errorf("data = %q, want %q", string(data), tt.wantData)
				}
				if errText != "" {
					t.Errorf("unexpected error text: %q", errText)
				}
				return
			}

			if data != nil {
				t.Errorf("expected nil data, got %q", string(data))
			}
			if !strings.Contains(errText, tt.wantErr) {
				t.Errorf("error text = %q, want it to contain %q", errText, tt.wantErr)
			}
		})
	}
}

// The regression this whole file guards: doRequest reports a non-2xx as a
// plain-text *result* rather than a Go error, so an aggregating handler sees
// err == nil and used to store that text in a json.RawMessage. The enclosing
// json.Marshal then failed, its error was discarded, and the entire report
// collapsed to "" - one sick endpoint erasing every healthy one, and an empty
// security report is indistinguishable from "nothing found".
func TestAggregateReportSurvivesOneFailingEndpoint(t *testing.T) {
	type row struct {
		Type  string          `json:"type"`
		Error string          `json:"error,omitempty"`
		Data  json.RawMessage `json:"data,omitempty"`
	}

	endpoints := []struct {
		name   string
		result *mcp.CallToolResult
	}{
		{"sast", toolResult(`{"findings":[{"id":"abc"}],"numItems":1}`)},
		{"cspm", toolError(fmt.Errorf(`API returned 500: {"message":"Internal Server Error"}`))},
	}

	var rows []row
	for _, ep := range endpoints {
		data, errText := aggregatePayload(ep.result)
		rows = append(rows, row{Type: ep.name, Data: data, Error: errText})
	}

	out, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatalf("aggregate must stay marshalable, got error: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("aggregate collapsed to an empty document")
	}

	var got []row
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("aggregate is not valid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}

	// The healthy endpoint still reports its findings. MarshalIndent re-indents
	// the embedded payload, so match on the values rather than exact bytes.
	if !strings.Contains(string(got[0].Data), `"numItems"`) ||
		!strings.Contains(string(got[0].Data), `"abc"`) {
		t.Errorf("healthy endpoint data lost: %q", string(got[0].Data))
	}
	// ...and the failing one is named as failed, not as empty.
	if got[1].Error == "" {
		t.Error("failing endpoint reported no error - it would read as 'nothing found'")
	}
	if got[1].Data != nil {
		t.Errorf("failing endpoint should carry no data, got %q", string(got[1].Data))
	}
}
