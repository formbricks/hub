package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpSmokeTimeout = 90 * time.Second
)

func TestMCPPackageSmoke(t *testing.T) {
	if os.Getenv("RUN_MCP_SMOKE_TEST") != "1" {
		t.Skip("set RUN_MCP_SMOKE_TEST=1 to run the live MCP package smoke test")
	}

	hubAPIKey := strings.TrimSpace(os.Getenv("HUB_API_KEY"))
	if hubAPIKey == "" {
		t.Fatal("HUB_API_KEY is required")
	}

	hubBaseURL := strings.TrimSpace(os.Getenv("FORMBRICKS_HUB_BASE_URL"))
	if hubBaseURL == "" {
		hubBaseURL = strings.TrimSpace(os.Getenv("HUB_BASE_URL"))
	}

	if hubBaseURL == "" {
		t.Fatal("FORMBRICKS_HUB_BASE_URL is required (HUB_BASE_URL is also accepted as a legacy alias)")
	}

	mcpPackage := strings.TrimSpace(os.Getenv("HUB_MCP_PACKAGE"))
	if mcpPackage == "" {
		mcpPackage = "@formbricks/hub-mcp@latest"
	}

	ctx, cancel := context.WithTimeout(context.Background(), mcpSmokeTimeout)
	defer cancel()

	stderr := &tailWriter{maxLines: 20}

	//nolint:gosec // opt-in release smoke test intentionally runs the configured MCP package.
	cmd := exec.CommandContext(ctx, "npx", "-y", mcpPackage)

	cmd.Env = append(os.Environ(),
		"HUB_API_KEY="+hubAPIKey,
		"FORMBRICKS_HUB_BASE_URL="+hubBaseURL,
		"HUB_BASE_URL="+hubBaseURL,
	)
	cmd.Stderr = stderr

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "formbricks-hub-mcp-smoke",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: 5 * time.Second,
	}, nil)
	if err != nil {
		failMCPTestf(t, stderr, "connect to MCP server: %v", err)
	}

	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Logf("close MCP session: %v", err)
		}
	})

	hasExecuteTool, err := mcpToolExists(ctx, session, "execute")
	if err != nil {
		failMCPTestf(t, stderr, "list MCP tools: %v", err)
	}

	if !hasExecuteTool {
		failMCPTestf(t, stderr, "required MCP tool %q not found", "execute")
	}

	executeResponse, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "execute",
		Arguments: map[string]string{
			"intent": "End-to-end smoke test: create, list, and delete a feedback record against a configured Formbricks Hub deployment",
			"code":   mcpSmokeCode(),
		},
	})
	if err != nil {
		failMCPTestf(t, stderr, "call execute tool: %v", err)
	}

	if executeResponse.IsError {
		failMCPTestf(t, stderr, "execute returned MCP tool error: %+v", executeResponse)
	}

	resultText, ok := firstTextContent(executeResponse)
	if !ok {
		failMCPTestf(t, stderr, "execute did not return text content: %+v", executeResponse)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText), &payload); err != nil {
		failMCPTestf(t, stderr, "decode execute text payload: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(payload["result"], &result); err != nil {
		failMCPTestf(t, stderr, "decode execute result payload: %v", err)
	}

	tenantID := decodeJSONValue[string](t, result["tenantID"])
	submissionID := decodeJSONValue[string](t, result["submissionID"])
	createdID := decodeJSONValue[string](t, result["createdID"])
	listedCount := decodeJSONValue[int](t, result["listedCount"])
	deleted := decodeJSONValue[bool](t, result["deleted"])

	if createdID == "" {
		failMCPTestf(t, stderr, "execute did not return createdID: %+v", result)
	}

	if listedCount < 1 {
		failMCPTestf(t, stderr, "expected listedCount >= 1: %+v", result)
	}

	if !deleted {
		failMCPTestf(t, stderr, "expected deleted=true: %+v", result)
	}

	t.Logf("MCP smoke test passed for tenant_id=%s submission_id=%s created_id=%s",
		tenantID, submissionID, createdID)
}

func TestMCPToolExistsFindsToolAcrossPages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "formbricks-hub-mcp-smoke-test-server",
		Version: "1.0.0",
	}, &mcp.ServerOptions{PageSize: 1})
	addMCPNoopTool(server, "alpha")
	addMCPNoopTool(server, "execute")

	session := connectTestMCPServer(ctx, t, server)

	exists, err := mcpToolExists(ctx, session, "execute")
	if err != nil {
		t.Fatalf("mcpToolExists() error = %v, want nil", err)
	}

	if !exists {
		t.Fatal("mcpToolExists() = false, want true")
	}

	missing, err := mcpToolExists(ctx, session, "missing")
	if err != nil {
		t.Fatalf("mcpToolExists(missing) error = %v, want nil", err)
	}

	if missing {
		t.Fatal("mcpToolExists(missing) = true, want false")
	}
}

func TestFirstTextContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "ok"},
		},
	}

	text, foundText := firstTextContent(result)
	if !foundText {
		t.Fatal("firstTextContent() ok = false, want true")
	}

	if text != "ok" {
		t.Fatalf("firstTextContent() text = %q, want ok", text)
	}

	_, foundText = firstTextContent(&mcp.CallToolResult{})
	if foundText {
		t.Fatal("firstTextContent(empty) ok = true, want false")
	}
}

func TestDecodeJSONValue(t *testing.T) {
	got := decodeJSONValue[map[string]int](t, json.RawMessage(`{"count":3}`))

	if got["count"] != 3 {
		t.Fatalf("decodeJSONValue() count = %d, want 3", got["count"])
	}
}

func TestTailWriterKeepsRecentLines(t *testing.T) {
	writer := &tailWriter{maxLines: 2}
	input := []byte("one\n\ntwo\nthree\n")

	writtenBytes, err := writer.Write(input)
	if err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	if writtenBytes != len(input) {
		t.Fatalf("Write() bytes = %d, want %d", writtenBytes, len(input))
	}

	if got := strings.Join(writer.lines, ","); got != "two,three" {
		t.Fatalf("tail lines = %q, want two,three", got)
	}
}

func TestMCPSmokeCodeBuildsExpectedWorkflow(t *testing.T) {
	code := mcpSmokeCode()
	wantSnippets := []string{
		"feedbackRecords.create",
		"feedbackRecords.list",
		"feedbackRecords.delete",
		"mcp-smoke-",
		"mcp-smoke-submission-",
		"deleted: true",
	}

	for _, want := range wantSnippets {
		if !strings.Contains(code, want) {
			t.Fatalf("mcpSmokeCode() missing %q in %s", want, code)
		}
	}
}

func mcpToolExists(ctx context.Context, session *mcp.ClientSession, toolName string) (bool, error) {
	params := &mcp.ListToolsParams{}
	for {
		tools, err := session.ListTools(ctx, params)
		if err != nil {
			return false, fmt.Errorf("list MCP tools: %w", err)
		}

		for _, tool := range tools.Tools {
			if tool.Name == toolName {
				return true, nil
			}
		}

		if tools.NextCursor == "" {
			return false, nil
		}

		params.Cursor = tools.NextCursor
	}
}

func firstTextContent(result *mcp.CallToolResult) (string, bool) {
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if ok {
			return text.Text, true
		}
	}

	return "", false
}

func connectTestMCPServer(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}

	t.Cleanup(func() {
		if err := serverSession.Close(); err != nil {
			t.Logf("close MCP server session: %v", err)
		}
	})

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "formbricks-hub-mcp-smoke-test-client",
		Version: "1.0.0",
	}, nil)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}

	t.Cleanup(func() {
		if err := clientSession.Close(); err != nil {
			t.Logf("close MCP client session: %v", err)
		}
	})

	return clientSession
}

func addMCPNoopTool(server *mcp.Server, name string) {
	mcp.AddTool(server, &mcp.Tool{Name: name},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "ok"},
				},
			}, nil, nil
		})
}

func decodeJSONValue[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode JSON value: %v", err)
	}

	return value
}

type tailWriter struct {
	mu       sync.Mutex
	maxLines int
	lines    []string
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for line := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}

		w.lines = append(w.lines, line)
		if len(w.lines) > w.maxLines {
			w.lines = w.lines[len(w.lines)-w.maxLines:]
		}
	}

	return len(p), nil
}

func failMCPTestf(t *testing.T, stderr *tailWriter, format string, args ...any) {
	t.Helper()

	stderr.mu.Lock()
	stderrLines := append([]string(nil), stderr.lines...)
	stderr.mu.Unlock()

	if len(stderrLines) > 0 {
		t.Log("recent MCP stderr:")

		for _, line := range stderrLines {
			t.Log(line)
		}
	}

	t.Fatalf(format, args...)
}

func mcpSmokeCode() string {
	timestamp := time.Now().UnixMilli()
	tenantID := fmt.Sprintf("mcp-smoke-%d", timestamp)
	submissionID := fmt.Sprintf("mcp-smoke-submission-%d", timestamp)

	return fmt.Sprintf(`async function run(client) {
  const tenantID = %q;
  const submissionID = %q;
  const created = await client.feedbackRecords.create({
    source_type: "survey",
    field_id: "mcp_smoke_test",
    field_type: "text",
    field_label: "MCP smoke test",
    value_text: "Hub MCP smoke test",
    source_id: "mcp-smoke",
    source_name: "Hub MCP Smoke Test",
    tenant_id: tenantID,
    submission_id: submissionID,
    user_identifier: submissionID,
    language: "en"
  });
  const listed = await client.feedbackRecords.list({
    tenant_id: tenantID,
    submission_id: submissionID,
    limit: 10
  });
  await client.feedbackRecords.delete(created.id);
  return {
    tenantID,
    submissionID,
    createdID: created.id,
    listedCount: listed.data.length,
    deleted: true
  };
}`, tenantID, submissionID)
}
