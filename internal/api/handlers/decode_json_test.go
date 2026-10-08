package handlers

import (
	"bytes"
	"context"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

var (
	// backslash is a backslash, built rather than written so no tool or editor can turn a JSON escape
	// written in this file into the character it stands for.
	backslash = string(rune(92))

	kelvinSign = string(rune(0x212A)) // Go's v1 decoder folds it onto "k"
	longS      = string(rune(0x017F)) // ... and this onto "s"
	// escapedT is a JSON escape for "T", spelled out so the source holds the escape itself.
	escapedT = backslash + "u0054"
)

type decodeProbe struct {
	TenantID    string             `json:"tenant_id"              validate:"omitempty,no_replacement_char"`
	Query       string             `json:"query"`
	Kind        string             `json:"kind"`
	Status      string             `json:"status"`
	Metadata    jsonv1.RawMessage  `json:"metadata,omitempty"`
	Diagnostics *decodeProbeDetail `json:"diagnostics,omitempty"`
	CollectedAt *time.Time         `json:"collected_at,omitempty"`
	Count       int                `json:"count,omitempty"`
}

type decodeProbeDetail struct {
	Model string `json:"model,omitempty"`
}

// decodeAndRespond runs decodeJSONBody and, on failure, the same response mapping every handler
// uses, so assertions see what a caller sees.
func decodeAndRespond(t *testing.T, body string, maxBytes int64) (decodeProbe, *httptest.ResponseRecorder, error) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/x", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var dst decodeProbe

	err := decodeJSONBody(rec, req, &dst, maxBytes)
	if err != nil {
		response.RespondError(rec, req, err)
	}

	return dst, rec, err
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) response.ProblemDetails {
	t.Helper()

	var problem response.ProblemDetails
	require.NoError(t, jsonv1.Unmarshal(rec.Body.Bytes(), &problem))

	return problem
}

// Pins why decodeJSONBody uses encoding/json/v2: the v1 decoder reads every one of these as one of
// the probe's fields. Kept as a test so a Go release that changes v1 is noticed.
func TestEncodingJSONV1FoldsMemberNames(t *testing.T) {
	cases := map[string]func(decodeProbe) string{
		`{"tenant_id":"A","TENANT_ID":"B"}`:                func(p decodeProbe) string { return p.TenantID },
		`{"tenant_id":"A","Tenant_Id":"B"}`:                func(p decodeProbe) string { return p.TenantID },
		`{"tenant_id":"A","` + escapedT + `ENANT_ID":"B"}`: func(p decodeProbe) string { return p.TenantID },
		`{"tenant_id":"A","tenant_id":"B"}`:                func(p decodeProbe) string { return p.TenantID },
		`{"tenant_id":"A"}{"tenant_id":"B"}`:               func(decodeProbe) string { return "B" }, // decodes A, ignores the rest
		`{"` + kelvinSign + `ind":"B"}`:                    func(p decodeProbe) string { return p.Kind },
		`{"` + longS + `tatus":"B"}`:                       func(p decodeProbe) string { return p.Status },
	}

	for body, field := range cases {
		t.Run(body, func(t *testing.T) {
			var probe decodeProbe

			decoder := jsonv1.NewDecoder(strings.NewReader(body))
			decoder.DisallowUnknownFields()
			require.NoError(t, decoder.Decode(&probe))
			assert.Equal(t, "B", field(probe))
		})
	}
}

func TestDecodeJSONBody(t *testing.T) {
	t.Run("decodes a canonical body", func(t *testing.T) {
		probe, _, err := decodeAndRespond(t, " \n{\"tenant_id\":\"A\",\"query\":\"q\",\"metadata\":{\"TENANT_ID\":\"x\",\"a\":1}}\t\n", 1<<10)

		require.NoError(t, err)
		assert.Equal(t, "A", probe.TenantID)
		assert.Equal(t, "q", probe.Query)
		assert.JSONEq(t, `{"TENANT_ID":"x","a":1}`, string(probe.Metadata), "free-form members are not matched against fields")
	})

	t.Run("accepts a canonical name written with an escape", func(t *testing.T) {
		probe, _, err := decodeAndRespond(t, `{"tenant`+backslash+`u005fid":"A"}`, 1<<10)

		require.NoError(t, err)
		assert.Equal(t, "A", probe.TenantID)
	})

	t.Run("decodes a JSON null as the zero value, as before", func(t *testing.T) {
		probe, _, err := decodeAndRespond(t, `null`, 1<<10)

		require.NoError(t, err)
		assert.Equal(t, decodeProbe{}, probe)
	})

	unknown := map[string]struct{ body, member string }{
		"upper-case variant after":            {`{"tenant_id":"A","TENANT_ID":"B"}`, "TENANT_ID"},
		"mixed-case variant before":           {`{"Tenant_Id":"B","tenant_id":"A"}`, "Tenant_Id"},
		"escaped variant":                     {`{"tenant_id":"A","` + escapedT + `ENANT_ID":"B"}`, "TENANT_ID"},
		"only the first letter upper-case":    {`{"tenant_id":"A","Tenant_id":"B"}`, "Tenant_id"},
		"upper-case after a lower-case start": {`{"tenant_id":"A","tenant_ID":"B"}`, "tenant_ID"},
		"variant alone":                       {`{"TENANT_ID":"B"}`, "TENANT_ID"},
		"kelvin sign":                         {`{"` + kelvinSign + `ind":"B"}`, kelvinSign + "ind"},
		"long s":                              {`{"` + longS + `tatus":"B"}`, longS + "tatus"},
		"nested variant":                      {`{"diagnostics":{"Model":"x"}}`, "diagnostics.Model"},
		"canonical but unknown":               {`{"tenant_id":"A","nope":1}`, "nope"},
	}

	for name, tt := range unknown {
		t.Run("refuses unknown member: "+name, func(t *testing.T) {
			_, rec, err := decodeAndRespond(t, tt.body, 1<<10)

			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, []response.InvalidParam{{Name: tt.member, Reason: response.ReasonJSONMemberUnknown}}, problemOf(t, rec).InvalidParams)
		})
	}

	repeated := map[string]struct{ body, member string }{
		"top level":            {`{"tenant_id":"A","tenant_id":"B"}`, "tenant_id"},
		"through an escape":    {`{"tenant_id":"A","tenant` + backslash + `u005fid":"B"}`, "tenant_id"},
		"inside a typed field": {`{"diagnostics":{"model":"a","model":"b"}}`, "diagnostics.model"},
		"inside free-form":     {`{"metadata":{"x":1,"x":2}}`, "metadata.x"},
	}

	for name, tt := range repeated {
		t.Run("refuses a repeated member: "+name, func(t *testing.T) {
			_, rec, err := decodeAndRespond(t, tt.body, 1<<10)

			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, []response.InvalidParam{{Name: tt.member, Reason: response.ReasonJSONMemberRepeated}}, problemOf(t, rec).InvalidParams)
		})
	}

	for name, body := range map[string]string{
		"a second object":   `{"tenant_id":"A"}{"tenant_id":"B"}`,
		"a second value":    `{"tenant_id":"A"} 1`,
		"garbage":           `{"tenant_id":"A"}x`,
		"a stray delimiter": `{"tenant_id":"A"}}`,
	} {
		t.Run("refuses trailing data: "+name, func(t *testing.T) {
			_, rec, err := decodeAndRespond(t, body, 1<<10)

			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			detail := problemOf(t, rec).Detail
			assert.True(t, strings.HasPrefix(detail, "Invalid JSON: "), detail)
			assert.Contains(t, detail, "after top-level value")
		})
	}

	for _, body := range []string{`[]`, `[{"tenant_id":"A"}]`, `"tenant_id"`, `1`, `true`} {
		t.Run("refuses a top-level "+body, func(t *testing.T) {
			_, rec, err := decodeAndRespond(t, body, 1<<10)

			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, "Invalid request body: must be a JSON object", problemOf(t, rec).Detail)
		})
	}

	for name, body := range map[string]string{"empty": ``, "whitespace": " \n\t\r "} {
		t.Run("reports an "+name+" body as io.EOF", func(t *testing.T) {
			_, rec, err := decodeAndRespond(t, body, 1<<10)

			require.ErrorIs(t, err, io.EOF)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, "Invalid request body", problemOf(t, rec).Detail)
		})
	}

	t.Run("does not treat a non-JSON space as an empty body", func(t *testing.T) {
		_, rec, err := decodeAndRespond(t, string(rune(0x00A0)), 1<<10)

		require.Error(t, err)
		require.NotErrorIs(t, err, io.EOF)
		assert.Contains(t, problemOf(t, rec).Detail, "Invalid JSON")
	})

	t.Run("keeps v1's handling of invalid UTF-8 in values: mangled, left to the validators", func(t *testing.T) {
		probe, _, err := decodeAndRespond(t, "{\"tenant_id\":\"A\xff\"}", 1<<10)

		require.NoError(t, err)
		assert.Equal(t, "A"+string(rune(0xFFFD)), probe.TenantID)
	})

	t.Run("refuses a member name holding invalid UTF-8 as unknown", func(t *testing.T) {
		_, rec, err := decodeAndRespond(t, "{\"tenant_id\xff\":\"A\"}", 1<<10)

		require.Error(t, err)
		require.Len(t, problemOf(t, rec).InvalidParams, 1)
		assert.Equal(t, response.ReasonJSONMemberUnknown, problemOf(t, rec).InvalidParams[0].Reason)
	})

	t.Run("reports a value of the wrong kind at its member", func(t *testing.T) {
		_, rec, _ := decodeAndRespond(t, `{"tenant_id":1}`, 1<<10)

		assert.Equal(t, []response.InvalidParam{{Name: "tenant_id", Reason: "must be string"}}, problemOf(t, rec).InvalidParams)
	})

	// v2's own time format is strict RFC 3339; request bodies keep v1's parsing so timestamps clients
	// already send still ingest.
	for _, stamp := range []string{"2024-01-01T00:00:00Z", "2024-01-01T1:00:00Z", "2024-01-01T00:00:00,123Z", "2024-01-01T00:00:00.5+02:00"} {
		t.Run("accepts the timestamp v1 accepted: "+stamp, func(t *testing.T) {
			probe, _, err := decodeAndRespond(t, `{"collected_at":"`+stamp+`"}`, 1<<10)

			require.NoError(t, err)
			require.NotNil(t, probe.CollectedAt)

			var want time.Time
			require.NoError(t, jsonv1.Unmarshal([]byte(`"`+stamp+`"`), &want))
			assert.True(t, want.Equal(*probe.CollectedAt))
		})
	}

	t.Run("a null timestamp leaves the field unset", func(t *testing.T) {
		probe, _, err := decodeAndRespond(t, `{"collected_at":null}`, 1<<10)

		require.NoError(t, err)
		assert.Nil(t, probe.CollectedAt)
	})

	for _, value := range []string{"1", "true", "{}", "[]"} {
		t.Run("names the expected type for a timestamp of the wrong kind: "+value, func(t *testing.T) {
			_, rec, _ := decodeAndRespond(t, `{"collected_at":`+value+`}`, 1<<10)

			assert.Equal(t, []response.InvalidParam{{Name: "collected_at", Reason: "must be time.Time"}},
				problemOf(t, rec).InvalidParams)
		})
	}

	t.Run("names a field holding an unparseable timestamp, with a bounded reason", func(t *testing.T) {
		_, rec, err := decodeAndRespond(t, `{"collected_at":"`+strings.Repeat("x", 600)+`"}`, 4<<10)

		require.Error(t, err)

		params := problemOf(t, rec).InvalidParams
		require.Len(t, params, 1)
		assert.Equal(t, "collected_at", params[0].Name)
		assert.LessOrEqual(t, len([]rune(params[0].Reason)), 257)
	})

	for _, number := range []string{"1.5", "1e2"} {
		t.Run("names the expected type for a number of the wrong form: "+number, func(t *testing.T) {
			_, rec, _ := decodeAndRespond(t, `{"count":`+number+`}`, 1<<10)

			assert.Equal(t, []response.InvalidParam{{Name: "count", Reason: "must be int"}}, problemOf(t, rec).InvalidParams)
		})
	}

	t.Run("reports malformed and truncated JSON", func(t *testing.T) {
		_, rec, _ := decodeAndRespond(t, `{"tenant_id":`, 1<<10)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "Invalid JSON: unexpected end of input", problemOf(t, rec).Detail)
	})

	t.Run("refuses an oversized body with 413", func(t *testing.T) {
		_, rec, err := decodeAndRespond(t, `{"query":"`+strings.Repeat("a", 2<<10)+`"}`, 1<<10)

		_, ok := errors.AsType[*http.MaxBytesError](err)
		require.True(t, ok, "want *http.MaxBytesError, got %v", err)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Equal(t, "request body too large", problemOf(t, rec).Detail)
	})

	t.Run("accepts a body exactly at the cap", func(t *testing.T) {
		body := `{"query":"` + strings.Repeat("a", 100) + `"}`
		_, _, err := decodeAndRespond(t, body, int64(len(body)))

		require.NoError(t, err)
	})

	t.Run("does not reserve a claimed Content-Length beyond the pre-size bound", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/x", strings.NewReader(`{"query":"q"}`))
		req.ContentLength = 16 << 20 // a claim, not what is sent

		var stats runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&stats)
		before := stats.TotalAlloc

		_ = decodeJSONBody(httptest.NewRecorder(), req, &decodeProbe{}, 16<<20)

		runtime.ReadMemStats(&stats)
		assert.Less(t, stats.TotalAlloc-before, uint64(maxPresizeBytes+(1<<20)), "allocated for the claimed length")
	})
}

// Refused member names are caller-controlled: they go to the caller in invalid_params and must not
// reach the log, which records each problem's detail.
func TestDecodeJSONBodyDoesNotLogMemberNames(t *testing.T) {
	var logs bytes.Buffer

	prev := slog.Default()

	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, body := range []string{
		`{"tenant_id":"A","SECRET_NAME":"B"}`,
		`{"SECRET_NAME":"A","SECRET_NAME":"B"}`,
		`{"diagnostics":{"SECRET_NAME":"x"}}`,
		`{"metadata":{"SECRET_NAME":1,"SECRET_NAME":2}}`,
		`{"tenant_id":"A"} {"SECRET_NAME":1}`,
		// A syntax error under a caller-chosen name: the error's own text would name the path.
		`{"metadata":{"SECRET_NAME":tru}}`,
	} {
		_, rec, _ := decodeAndRespond(t, body, 1<<10)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
	}

	require.NotEmpty(t, logs.String())
	assert.NotContains(t, logs.String(), "SECRET_NAME")
}

// Members of arrays are named the way the validator and the OpenAPI examples name them.
func TestDecodeJSONBodyNamesArrayMembersWithIndexes(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/x",
		strings.NewReader(`{"clusters":[],"nodes":[],"memberships":[{"cluster_key":1,"feedback_record_id":"not-a-uuid"}]}`))
	rec := httptest.NewRecorder()

	err := decodeJSONBody(rec, req, &models.TaxonomyRunResultRequest{}, 1<<10)
	require.Error(t, err)
	response.RespondError(rec, req, err)

	params := problemOf(t, rec).InvalidParams
	require.Len(t, params, 1)
	assert.Equal(t, "memberships[0].feedback_record_id", params[0].Name)
}

// Custom decoders (webhook requests, Optional) return the decoder's errors unwrapped, so v2 keeps
// the member's position: a wrong-type member is named, not reported against the whole body.
func TestCustomDecodersReportTheMemberPosition(t *testing.T) {
	cases := map[string]struct {
		body   string
		dst    any
		member string
	}{
		"webhook create": {`{"url":1,"tenant_id":"t"}`, &models.CreateWebhookRequest{}, "url"},
		"webhook update": {`{"url":1}`, &models.UpdateWebhookRequest{}, "url"},
		"settings patch": {`{"target_language":123}`, &models.PatchTenantSettingsRequest{}, "target_language"},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/x",
				strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			err := decodeJSONBody(rec, req, tt.dst, 1<<10)
			require.Error(t, err)
			response.RespondError(rec, req, err)

			assert.Equal(t, []response.InvalidParam{{Name: tt.member, Reason: "must be string"}}, problemOf(t, rec).InvalidParams)
		})
	}
}

// decodedRequestTypes lists every type passed to decodeJSONBody.
var decodedRequestTypes = []any{
	&models.CreateFeedbackRecordRequest{},
	&models.UpdateFeedbackRecordRequest{},
	&SemanticSearchRequest{},
	&models.CreateWebhookRequest{},
	&models.UpdateWebhookRequest{},
	&models.CreateTaxonomyRunRequest{},
	&models.RenameTaxonomyNodeRequest{},
	&models.TaxonomyRunResultRequest{},
	&models.TaxonomyRunFailedRequest{},
	&models.UpdateTenantSettingsRequest{},
	&models.PatchTenantSettingsRequest{},
	&enrichmentRetryRequest{},
}

// Every request type must decode under v2's rules: a struct whose definition v2 rejects (conflicting
// names, unsupported options) would fail every request at run time, not at build time.
func TestDecodedRequestTypesDecodeUnderV2(t *testing.T) {
	for _, dst := range decodedRequestTypes {
		t.Run(reflect.TypeOf(dst).Elem().Name(), func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/x", strings.NewReader(`{}`))

			require.NoError(t, decodeJSONBody(httptest.NewRecorder(), req, dst, 1<<10))
		})
	}
}

// FuzzDecodeJSONBody checks the property the gateway relies on: whatever decodeJSONBody accepts,
// the tenant it decodes is the value of the exact "tenant_id" member — never a case variant.
func FuzzDecodeJSONBody(f *testing.F) {
	for _, seed := range []string{
		`{"tenant_id":"A","query":"q"}`,
		`{"tenant_id":"A","TENANT_ID":"B"}`,
		`{"TENANT_ID":"B","tenant_id":"A"}`,
		`{"tenant_id":"A","Tenant_Id":"B"}`,
		`{"tenant_id":"A","tenant_ID":"B"}`,
		`{"tenant_id":"A","` + escapedT + `ENANT_ID":"B"}`,
		`{"tenant_id":"A","tenant_id":"B"}`,
		`{"tenant_id":"A"}{"tenant_id":"B"}`,
		`{"tenant_id":null}`,
		`{"query":"q","metadata":{"TENANT_ID":"B"}}`,
		`{"tenant_id":"A","` + kelvinSign + `ind":"B"}`,
		`{"tenant_id":"A` + "\xe2\x82" + `"}`,
		`{"tenant_id":"A` + backslash + `ud800"}`,
		`null`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/x", bytes.NewReader(body))

		var probe decodeProbe
		if !decodeAndValidateJSONBody(httptest.NewRecorder(), req, &probe, 1<<16) {
			return
		}

		// An accepted body has unique member names, so an exact-key map reads it unambiguously.
		var members map[string]jsonv1.RawMessage
		require.NoError(t, jsonv1.Unmarshal(body, &members))

		// The accepted tenant must be what any JSON reader reads: v2's defaults refuse invalid UTF-8
		// and unpaired surrogates, the inputs readers disagree on.
		want := ""
		if raw, ok := members["tenant_id"]; ok && string(raw) != "null" {
			require.NoError(t, json.Unmarshal(raw, &want), "accepted a tenant only a lenient reader reads")
		}

		assert.Equal(t, want, probe.TenantID)

		for name := range members {
			if name != "tenant_id" {
				assert.NotEqual(t, "tenant_id", strings.ToLower(name), "accepted a variant spelling %q", name)
			}
		}
	})
}

// BenchmarkDecodeJSONBody compares the strict decode with the plain encoding/json decode it
// replaced, on a feedback record at the 512 KiB cap.
func BenchmarkDecodeJSONBody(b *testing.B) {
	body := []byte(`{"source_type":"survey","field_id":"q1","field_type":"text","tenant_id":"t",` +
		`"submission_id":"s","metadata":{"k":"` + strings.Repeat("a", maxFeedbackRecordBodyBytes-256) + `"}}`)

	b.Run("strict_v2", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()

		for b.Loop() {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/x", bytes.NewReader(body))

			var dst models.CreateFeedbackRecordRequest
			if err := decodeJSONBody(httptest.NewRecorder(), req, &dst, maxFeedbackRecordBodyBytes); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("plain_v1_baseline", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()

		for b.Loop() {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/x", bytes.NewReader(body))
			req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, maxFeedbackRecordBodyBytes)

			var dst models.CreateFeedbackRecordRequest

			decoder := jsonv1.NewDecoder(req.Body)
			decoder.DisallowUnknownFields()

			if err := decoder.Decode(&dst); err != nil {
				b.Fatal(err)
			}
		}
	})
}
