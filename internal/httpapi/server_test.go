package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Schallophy/baton/internal/sessions"
)

// newTestServer builds the real handler around a detector that never touches
// disk: the tests only exercise routing, headers and body parsing.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	detector := &sessions.Detector{}
	return NewServer(detector, "test")
}

func TestRouterMatchesExactPaths(t *testing.T) {
	server := newTestServer(t)
	cases := map[string]bool{
		"GET /api/meta":     true,
		"GET /api/sessions": true,
		"GET /api/session":  true,
		"POST /api/handoff": true,
		"GET /api/handoff":  false, // method matters, not just the path
		"GET /api/nope":     false,
		"GET /api":          false,
	}
	for key, want := range cases {
		method, path, _ := strings.Cut(key, " ")
		got := server.match(method, path) != nil
		if got != want {
			t.Fatalf("%s matched = %v, want %v", key, got, want)
		}
	}
}

func TestNonLoopbackIsRejected(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest("GET", "/api/sessions", nil)
	request.RemoteAddr = "192.168.1.7:5000"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != 403 {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if got := recorder.Header().Get("content-security-policy"); got == "" {
		t.Fatalf("CSP header missing on the rejection path")
	}
}

func TestStaticAssetsAreNoStore(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest("GET", "/js/main.js", nil)
	request.RemoteAddr = "127.0.0.1:1"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("cache-control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("content-type"); !strings.HasPrefix(got, "text/javascript") {
		t.Fatalf("content-type = %q", got)
	}
}

func TestStaticRejectsTraversalAndMissingFiles(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/../go.mod", "/js/../../main.go", "/nope.js"} {
		request := httptest.NewRequest("GET", path, nil)
		request.RemoteAddr = "127.0.0.1:1"
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code == 200 {
			t.Fatalf("%s was served (status 200)", path)
		}
	}
}

func TestHandoffRejectsNonJSONBody(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest("POST", "/api/handoff", strings.NewReader("not json"))
	request.RemoteAddr = "127.0.0.1:1"
	request.Header.Set("content-type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "JSON") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestHandoffAcceptsUTF8BOM(t *testing.T) {
	// PowerShell 的 Out-File 会给 JSON 加 BOM；脚本用 curl 发请求很常见。
	server := newTestServer(t)
	payload := []byte("\xef\xbb\xbf")
	payload = append(payload, []byte(`{"agent":"opencode","id":"missing"}`)...)
	request := httptest.NewRequest("POST", "/api/handoff", bytes.NewReader(payload))
	request.RemoteAddr = "127.0.0.1:1"
	request.Header.Set("content-type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	// 解析成功后才会走到「会话不存在」；不能停在 400「不是合法 JSON」。
	if recorder.Code == 400 {
		t.Fatalf("BOM was treated as invalid JSON: %s", recorder.Body.String())
	}
}

func TestHandoffRequiresAgentAndID(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest("POST", "/api/handoff", strings.NewReader(`{"agent":"opencode"}`))
	request.RemoteAddr = "127.0.0.1:1"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("error body is not JSON: %v", err)
	}
	if payload.Error.Message == "" {
		t.Fatalf("empty error message")
	}
}

func TestReasonCodeNormalises(t *testing.T) {
	cases := map[string]string{
		"":         sessions.ReasonDetected,
		"quota":    sessions.ReasonQuota,
		" QUOTA ":  sessions.ReasonQuota,
		"upstream": sessions.ReasonUpstream,
		"aborted":  sessions.ReasonAborted,
		"switch":   sessions.ReasonSwitch,
		"other":    sessions.ReasonOther,
		"nonsense": sessions.ReasonDetected,
	}
	for input, want := range cases {
		if got := reasonCode(input); got != want {
			t.Fatalf("reasonCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIntValueFallsBack(t *testing.T) {
	body := map[string]any{"limit": float64(2500)}
	if got := intValue(body, "limit", 40000); got != 2500 {
		t.Fatalf("limit = %d", got)
	}
	if got := intValue(body, "missing", 40000); got != 40000 {
		t.Fatalf("missing = %d, want fallback", got)
	}
	if got := intValue(map[string]any{"limit": "2500"}, "limit", 40000); got != 40000 {
		t.Fatalf("string value did not fall back")
	}
}

func TestHTTPErrorsAreJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusBadRequest, "缺点东西")
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if recorder.Header().Get("content-type") != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", recorder.Header().Get("content-type"))
	}
}
