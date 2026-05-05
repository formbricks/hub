package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpSmokeTimeout         = 90 * time.Second
	mcpHTTPStartupTimeout   = 30 * time.Second
	mcpHTTPHealthTimeout    = 2 * time.Second
	mcpHTTPShutdownTimeout  = 5 * time.Second
	mcpHTTPHealthPollPeriod = 100 * time.Millisecond
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

	commandConfig := mcpSmokeCommandConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), mcpSmokeTimeout)
	defer cancel()

	output := &tailWriter{maxLines: 40}
	endpoint := startMCPHTTPServer(ctx, t, commandConfig, hubBaseURL, output)
	session := connectMCPHTTPClient(ctx, t, endpoint, hubAPIKey, output)

	runMCPSmokeWorkflow(ctx, t, session, output)
}

func runMCPSmokeWorkflow(ctx context.Context, t *testing.T, session *mcp.ClientSession, output *tailWriter) {
	t.Helper()

	hasExecuteTool, err := mcpToolExists(ctx, session, "execute")
	if err != nil {
		failMCPTestf(t, output, "list MCP tools: %v", err)
	}

	if !hasExecuteTool {
		failMCPTestf(t, output, "required MCP tool %q not found", "execute")
	}

	executeResponse, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "execute",
		Arguments: map[string]string{
			"intent": "End-to-end smoke test: create, list, and delete a feedback record against a configured Formbricks Hub deployment",
			"code":   mcpSmokeCode(),
		},
	})
	if err != nil {
		failMCPTestf(t, output, "call execute tool: %v", err)
	}

	if executeResponse.IsError {
		failMCPTestf(t, output, "execute returned MCP tool error: %+v", executeResponse)
	}

	resultText, ok := firstTextContent(executeResponse)
	if !ok {
		failMCPTestf(t, output, "execute did not return text content: %+v", executeResponse)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText), &payload); err != nil {
		failMCPTestf(t, output, "decode execute text payload: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(payload["result"], &result); err != nil {
		failMCPTestf(t, output, "decode execute result payload: %v", err)
	}

	tenantID := decodeJSONValue[string](t, result["tenantID"])
	submissionID := decodeJSONValue[string](t, result["submissionID"])
	createdID := decodeJSONValue[string](t, result["createdID"])
	listedCount := decodeJSONValue[int](t, result["listedCount"])
	deleted := decodeJSONValue[bool](t, result["deleted"])

	if createdID == "" {
		failMCPTestf(t, output, "execute did not return createdID: %+v", result)
	}

	if listedCount < 1 {
		failMCPTestf(t, output, "expected listedCount >= 1: %+v", result)
	}

	if !deleted {
		failMCPTestf(t, output, "expected deleted=true: %+v", result)
	}

	t.Logf("MCP smoke test passed for tenant_id=%s submission_id=%s created_id=%s",
		tenantID, submissionID, createdID)
}

type mcpCommandConfig struct {
	command string
	args    []string
	display string
}

func mcpSmokeCommandConfig(t *testing.T) mcpCommandConfig {
	t.Helper()

	config, err := mcpSmokeCommandConfigFromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}

	return config
}

func mcpSmokeCommandConfigFromEnv(getenv func(string) string) (mcpCommandConfig, error) {
	command := strings.TrimSpace(getenv("HUB_MCP_COMMAND"))
	if command != "" {
		args := strings.Fields(getenv("HUB_MCP_ARGS"))

		return mcpCommandConfig{
			command: command,
			args:    args,
			display: strings.Join(append([]string{command}, args...), " "),
		}, nil
	}

	packageRef := strings.TrimSpace(getenv("HUB_MCP_PACKAGE"))
	if packageRef == "" {
		return mcpCommandConfig{},
			errors.New("HUB_MCP_PACKAGE or HUB_MCP_COMMAND is required so the smoke test validates the MCP build under review")
	}

	return mcpCommandConfig{
		command: "npx",
		args:    []string{"-y", packageRef},
		display: "npx -y " + packageRef,
	}, nil
}

func startMCPHTTPServer(
	ctx context.Context,
	t *testing.T,
	commandConfig mcpCommandConfig,
	hubBaseURL string,
	output *tailWriter,
) string {
	t.Helper()

	port := freeLocalPort(ctx, t)

	args := append([]string{}, commandConfig.args...)
	args = append(args, "--transport=http", "--port", strconv.Itoa(port))

	//nolint:gosec // opt-in smoke test intentionally runs the configured MCP package or local command.
	cmd := exec.CommandContext(ctx, commandConfig.command, args...)

	cmd.Env = append(envWithout(os.Environ(), "HUB_API_KEY"),
		"FORMBRICKS_HUB_BASE_URL="+hubBaseURL,
		"HUB_BASE_URL="+hubBaseURL,
	)
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		failMCPTestf(t, output, "start MCP HTTP server (%s): %v", commandConfig.display, err)
	}

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	t.Cleanup(func() {
		stopMCPHTTPServer(t, cmd, done)
	})

	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := waitForMCPHTTPHealth(ctx, endpoint); err != nil {
		failMCPTestf(t, output, "wait for MCP HTTP server (%s): %v", commandConfig.display, err)
	}

	return endpoint
}

func connectMCPHTTPClient(
	ctx context.Context,
	t *testing.T,
	endpoint string,
	hubAPIKey string,
	output *tailWriter,
) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "formbricks-hub-mcp-smoke",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: authHeaderTransport{apiKey: hubAPIKey}},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		failMCPTestf(t, output, "connect to remote MCP server: %v", err)
	}

	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Logf("close MCP session: %v", err)
		}
	})

	return session
}

func freeLocalPort(ctx context.Context, t *testing.T) int {
	t.Helper()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port: %v", err)
	}

	defer func() {
		if err := listener.Close(); err != nil {
			t.Fatalf("close local port reservation: %v", err)
		}
	}()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("local listener address type = %T, want *net.TCPAddr", listener.Addr())
	}

	return addr.Port
}

func waitForMCPHTTPHealth(ctx context.Context, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, mcpHTTPStartupTimeout)
	defer cancel()

	client := &http.Client{Timeout: mcpHTTPHealthTimeout}

	ticker := time.NewTicker(mcpHTTPHealthPollPeriod)
	defer ticker.Stop()

	for {
		if mcpHTTPHealthReady(ctx, client, endpoint) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("health check did not pass: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func mcpHTTPHealthReady(ctx context.Context, client *http.Client, endpoint string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health", nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	return resp.StatusCode == http.StatusOK
}

func stopMCPHTTPServer(t *testing.T, cmd *exec.Cmd, done <-chan error) {
	t.Helper()

	if cmd.Process == nil {
		return
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Logf("interrupt MCP HTTP server: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Logf("MCP HTTP server exited: %v", err)
		}
	case <-time.After(mcpHTTPShutdownTimeout):
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("kill MCP HTTP server: %v", err)
		}

		if err := <-done; err != nil {
			t.Logf("MCP HTTP server exited after kill: %v", err)
		}
	}
}

func envWithout(env []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}

	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			filtered = append(filtered, entry)

			continue
		}

		if _, found := blocked[name]; found {
			continue
		}

		filtered = append(filtered, entry)
	}

	return filtered
}

type authHeaderTransport struct {
	apiKey string
	next   http.RoundTripper
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (t authHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}

	clonedReq := req.Clone(req.Context())
	clonedReq.Header = req.Header.Clone()
	clonedReq.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := next.RoundTrip(clonedReq)
	if err != nil {
		return nil, fmt.Errorf("send authenticated MCP HTTP request: %w", err)
	}

	return resp, nil
}

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func TestMCPSmokeCommandConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    mcpCommandConfig
		wantErr bool
	}{
		{
			name:    "requires explicit package or command",
			env:     map[string]string{},
			want:    mcpCommandConfig{},
			wantErr: true,
		},
		{
			name: "uses package through npx",
			env: map[string]string{
				"HUB_MCP_PACKAGE": "@formbricks/hub-mcp@0.0.1",
			},
			want: mcpCommandConfig{
				command: "npx",
				args:    []string{"-y", "@formbricks/hub-mcp@0.0.1"},
				display: "npx -y @formbricks/hub-mcp@0.0.1",
			},
		},
		{
			name: "uses local command before package",
			env: map[string]string{
				"HUB_MCP_COMMAND": "node",
				"HUB_MCP_ARGS":    "/workspace/hub-typescript/packages/mcp-server/dist/index.js",
				"HUB_MCP_PACKAGE": "@formbricks/hub-mcp@latest",
			},
			want: mcpCommandConfig{
				command: "node",
				args:    []string{"/workspace/hub-typescript/packages/mcp-server/dist/index.js"},
				display: "node /workspace/hub-typescript/packages/mcp-server/dist/index.js",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mcpSmokeCommandConfigFromEnv(func(key string) string {
				return tt.env[key]
			})

			if tt.wantErr {
				if err == nil {
					t.Fatal("mcpSmokeCommandConfigFromEnv() error = nil, want error")
				}

				return
			}

			if err != nil {
				t.Fatalf("mcpSmokeCommandConfigFromEnv() error = %v, want nil", err)
			}

			gotArgs := strings.Join(got.args, "\x00")
			wantArgs := strings.Join(tt.want.args, "\x00")

			if got.command != tt.want.command || got.display != tt.want.display || gotArgs != wantArgs {
				t.Fatalf("mcpSmokeCommandConfigFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEnvWithoutRemovesNamedVariables(t *testing.T) {
	got := envWithout([]string{
		"HUB_API_KEY=secret",
		"PATH=/bin",
		"FORMBRICKS_HUB_BASE_URL=https://hub.example.com",
	}, "HUB_API_KEY")

	if strings.Join(got, ",") != "PATH=/bin,FORMBRICKS_HUB_BASE_URL=https://hub.example.com" {
		t.Fatalf("envWithout() = %v, want HUB_API_KEY removed", got)
	}
}

func TestAuthHeaderTransportAddsAuthorization(t *testing.T) {
	transport := authHeaderTransport{
		apiKey: "test-key",
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("Authorization header = %q, want Bearer test-key", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:3000", nil)
	req.Header.Set("Authorization", "Bearer old")

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()
}

func TestRedactMCPLogLine(t *testing.T) {
	t.Setenv("HUB_API_KEY", "hub-secret")
	t.Setenv("API_KEY", "api-secret")
	t.Setenv("STAINLESS_API_KEY", "stainless-secret")

	got := redactMCPLogLine("hub-secret api-secret stainless-secret safe")
	if got != "[REDACTED] [REDACTED] [REDACTED] safe" {
		t.Fatalf("redactMCPLogLine() = %q, want all configured secrets redacted", got)
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
		"user_id: submissionID",
		"finally",
		"await client.feedbackRecords.delete(created.id)",
		"cleanupError",
	}

	for _, want := range wantSnippets {
		if !strings.Contains(code, want) {
			t.Fatalf("mcpSmokeCode() missing %q in %s", want, code)
		}
	}

	if strings.Contains(code, "user_identifier") {
		t.Fatalf("mcpSmokeCode() contains deprecated user_identifier field: %s", code)
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
			t.Log(redactMCPLogLine(line))
		}
	}

	t.Fatalf(format, args...)
}

func redactMCPLogLine(line string) string {
	redacted := line

	for _, secret := range []string{
		strings.TrimSpace(os.Getenv("HUB_API_KEY")),
		strings.TrimSpace(os.Getenv("API_KEY")),
		strings.TrimSpace(os.Getenv("STAINLESS_API_KEY")),
	} {
		if secret == "" {
			continue
		}

		redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
	}

	return redacted
}

func mcpSmokeCode() string {
	timestamp := time.Now().UnixMilli()
	tenantID := fmt.Sprintf("mcp-smoke-%d", timestamp)
	submissionID := fmt.Sprintf("mcp-smoke-submission-%d", timestamp)

	return fmt.Sprintf(`async function run(client) {
  const tenantID = %q;
  const submissionID = %q;
  let created = null;
  let listedCount = 0;
  let deleted = false;
  let cleanupError = null;
  try {
    created = await client.feedbackRecords.create({
      source_type: "survey",
      field_id: "mcp_smoke_test",
      field_type: "text",
      field_label: "MCP smoke test",
      value_text: "Hub MCP smoke test",
      source_id: "mcp-smoke",
      source_name: "Hub MCP Smoke Test",
      tenant_id: tenantID,
      submission_id: submissionID,
      user_id: submissionID,
      language: "en"
    });
    const listed = await client.feedbackRecords.list({
      tenant_id: tenantID,
      submission_id: submissionID,
      limit: 10
    });
    listedCount = listed.data.length;
  } finally {
    if (created?.id) {
      try {
        await client.feedbackRecords.delete(created.id);
        deleted = true;
      } catch (error) {
        cleanupError = error?.message ?? String(error);
        console.log("failed to delete MCP smoke feedback record", cleanupError);
      }
    }
  }
  return {
    tenantID,
    submissionID,
    createdID: created?.id ?? "",
    listedCount,
    deleted,
    ...(cleanupError && { cleanupError })
  };
}`, tenantID, submissionID)
}
