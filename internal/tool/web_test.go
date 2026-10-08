package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/afterlune/lune/internal/model"
)

func TestWebSearchToolParsesDuckDuckGoLiteResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/lite/" {
			t.Errorf("request = %s %s, want GET /lite/", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "weather & forecast" {
			t.Errorf("query = %q, want decoded search query", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><table>
			<tr><td><a class='result-link' href='https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fguide&amp;rut=abc'>A <b>guide</b></a></td></tr>
			<tr><td class='result-snippet'>Useful &amp; <b>clear</b> description.</td></tr>
			<tr><td><a class='result-link' href='https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2F&amp;rut=def'>Second result</a></td></tr>
			<tr><td class='result-snippet'>Second snippet.</td></tr>
		</table>`)
	}))
	defer server.Close()

	result := (WebSearchTool{httpClient: server.Client(), endpoint: server.URL + "/lite/"}).Run(
		context.Background(), json.RawMessage(`{"query":"weather & forecast","max_results":1}`),
	)
	if result.Err != nil {
		t.Fatalf("web_search: %v", result.Err)
	}
	var results []webSearchResult
	if err := json.Unmarshal([]byte(result.Output), &results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %s", len(results), result.Output)
	}
	if results[0].Title != "A guide" || results[0].URL != "https://example.com/guide" || results[0].Snippet != "Useful & clear description." {
		t.Fatalf("unexpected result: %#v", results[0])
	}
}

func TestWebSearchToolValidatesArgumentsAndHTTPStatus(t *testing.T) {
	for _, raw := range []string{
		`{"query":"topic","max_results":0}`,
		`{"query":"topic","max_results":11}`,
		`{"query":"  ","max_results":5}`,
	} {
		if result := (WebSearchTool{}).Run(context.Background(), json.RawMessage(raw)); result.Err == nil {
			t.Errorf("web_search(%s) should fail", raw)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	result := (WebSearchTool{httpClient: server.Client(), endpoint: server.URL}).Run(
		context.Background(), json.RawMessage(`{"query":"topic","max_results":5}`),
	)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "503") {
		t.Fatalf("error = %v, want HTTP 503", result.Err)
	}
}

func TestWebSearchToolReturnsEmptyArrayWhenNoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>No results</body></html>")
	}))
	defer server.Close()

	result := (WebSearchTool{httpClient: server.Client(), endpoint: server.URL}).Run(
		context.Background(), json.RawMessage(`{"query":"nothing","max_results":5}`),
	)
	if result.Err != nil {
		t.Fatalf("web_search: %v", result.Err)
	}
	if result.Output != "[]" {
		t.Fatalf("output = %q, want []", result.Output)
	}
}

func TestWebFetchToolExtractsHTMLText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>A &amp; title</title><script>head secret</script></head>
		<body><h1>Readable <b>page</b></h1><p>Text &amp; more.</p><script>body secret</script><style>hidden style</style></body></html>`)
	}))
	defer server.Close()

	result := (WebFetchTool{httpClient: server.Client()}).Run(
		context.Background(), json.RawMessage(`{"url":"`+server.URL+`/article"}`),
	)
	if result.Err != nil {
		t.Fatalf("web_fetch: %v", result.Err)
	}
	for _, want := range []string{"Title: A & title", "Readable page", "Text & more."} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("output %q does not contain %q", result.Output, want)
		}
	}
	for _, unwanted := range []string{"head secret", "body secret", "hidden style"} {
		if strings.Contains(result.Output, unwanted) {
			t.Errorf("output contains hidden content %q: %s", unwanted, result.Output)
		}
	}
}

func TestWebFetchToolSupportsTextAndJSONAndCapsOutput(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "plain text", contentType: "text/plain", body: "plain body"},
		{name: "json", contentType: "application/json", body: `{"ok":true}`},
		{name: "truncated text", contentType: "text/plain", body: strings.Repeat("界", maxWebTextRunes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			result := (WebFetchTool{httpClient: server.Client()}).Run(
				context.Background(), json.RawMessage(`{"url":"`+server.URL+`"}`),
			)
			if result.Err != nil {
				t.Fatalf("web_fetch: %v", result.Err)
			}
			if tt.name == "truncated text" {
				if !strings.Contains(result.Output, "[content truncated at 20000 characters]") {
					t.Fatalf("output was not marked as truncated")
				}
				return
			}
			if result.Output != tt.body {
				t.Fatalf("output = %q, want %q", result.Output, tt.body)
			}
		})
	}
}

func TestWebFetchToolRejectsInvalidURLsStatusesAndBinaryContent(t *testing.T) {
	if result := (WebFetchTool{}).Run(context.Background(), json.RawMessage(`{"url":"file:///tmp/secret"}`)); result.Err == nil {
		t.Fatal("expected non-HTTP URL to fail")
	}
	if result := (WebFetchTool{}).Run(context.Background(), json.RawMessage(`{"url":"http://user:pass@example.com/"}`)); result.Err == nil {
		t.Fatal("expected embedded credentials to fail")
	}

	for _, tt := range []struct {
		name        string
		status      int
		contentType string
		wantError   string
	}{
		{name: "not found", status: http.StatusNotFound, contentType: "text/html", wantError: "404"},
		{name: "pdf", status: http.StatusOK, contentType: "application/pdf", wantError: "unsupported content type"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, "body")
			}))
			defer server.Close()

			result := (WebFetchTool{httpClient: server.Client()}).Run(
				context.Background(), json.RawMessage(`{"url":"`+server.URL+`"}`),
			)
			if result.Err == nil || !strings.Contains(result.Err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want it to contain %q", result.Err, tt.wantError)
			}
		})
	}
}

func TestWebFetchToolRespectsTimeoutAndResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	result := (WebFetchTool{httpClient: &http.Client{Timeout: 20 * time.Millisecond}}).Run(
		context.Background(), json.RawMessage(`{"url":"`+server.URL+`"}`),
	)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "deadline exceeded") {
		t.Fatalf("error = %v, want request timeout", result.Err)
	}

	if _, err := readWebBody(strings.NewReader(strings.Repeat("x", maxWebResponse+1))); err == nil {
		t.Fatal("expected oversized response body to fail")
	}
}

func TestDefaultRegistryIncludesWebTools(t *testing.T) {
	registry, err := NewDefaultRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDefaultRegistry: %v", err)
	}
	want := map[string]bool{
		"command": false, "read_file": false, "write_file": false,
		"edit_file": false, "web_search": false, "web_fetch": false,
	}
	for _, definition := range registry.Definitions() {
		function, ok := definition.(model.FunctionTool)
		if !ok {
			t.Errorf("tool definition has type %T, want model.FunctionTool", definition)
			continue
		}
		if _, exists := want[function.Name]; !exists {
			t.Errorf("unexpected tool %q", function.Name)
			continue
		}
		want[function.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("default registry is missing %q", name)
		}
	}
}
