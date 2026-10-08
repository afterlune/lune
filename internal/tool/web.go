package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/afterlune/lune/internal/model"
	"golang.org/x/net/html"
)

const (
	duckDuckGoLiteURL = "https://lite.duckduckgo.com/lite/"
	webRequestTimeout = 15 * time.Second
	maxWebResponse    = 2 << 20
	maxWebTextRunes   = 20_000
	webUserAgent      = "lune-agent"

	webSearchSchema = `{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"max_results":{"type":"integer","minimum":1,"maximum":10,"description":"Number of results to return (1-10)"}},"required":["query","max_results"],"additionalProperties":false}`
	webFetchSchema  = `{"type":"object","properties":{"url":{"type":"string","description":"Absolute HTTP or HTTPS URL to fetch"}},"required":["url"],"additionalProperties":false}`
)

var defaultWebHTTPClient = &http.Client{Timeout: webRequestTimeout}

// WebSearchTool searches DuckDuckGo Lite and returns a JSON array of results.
type WebSearchTool struct {
	httpClient *http.Client
	endpoint   string
}

func (WebSearchTool) Definition() model.FunctionTool {
	return functionDefinition("web_search", "Search the web with DuckDuckGo and return result titles, URLs, and snippets.", webSearchSchema)
}

func (t WebSearchTool) Run(ctx context.Context, raw json.RawMessage) Result {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("invalid web_search arguments: %w", err)}
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return Result{Err: errors.New("query must not be empty")}
	}
	if args.MaxResults < 1 || args.MaxResults > 10 {
		return Result{Err: errors.New("max_results must be between 1 and 10")}
	}

	endpoint := t.endpoint
	if endpoint == "" {
		endpoint = duckDuckGoLiteURL
	}
	searchURL, err := url.Parse(endpoint)
	if err != nil || !searchURL.IsAbs() || searchURL.Host == "" || (searchURL.Scheme != "http" && searchURL.Scheme != "https") {
		return Result{Err: errors.New("invalid DuckDuckGo search endpoint")}
	}
	query := searchURL.Query()
	query.Set("q", args.Query)
	searchURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return Result{Err: fmt.Errorf("create web_search request: %w", err)}
	}
	request.Header.Set("User-Agent", webUserAgent)
	request.Header.Set("Accept", "text/html")

	response, err := t.client().Do(request)
	if err != nil {
		return Result{Err: fmt.Errorf("search DuckDuckGo: %w", err)}
	}
	defer response.Body.Close()
	body, err := readWebBody(response.Body)
	if err != nil {
		return Result{Err: fmt.Errorf("read DuckDuckGo response: %w", err)}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Result{Err: fmt.Errorf("DuckDuckGo returned %s", response.Status)}
	}

	results, err := parseDuckDuckGoResults(body, args.MaxResults)
	if err != nil {
		return Result{Err: fmt.Errorf("parse DuckDuckGo results: %w", err)}
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		return Result{Err: fmt.Errorf("encode web_search results: %w", err)}
	}
	return Result{Output: string(encoded)}
}

// WebFetchTool fetches an HTML or text page and returns readable text.
type WebFetchTool struct {
	httpClient *http.Client
}

func (WebFetchTool) Definition() model.FunctionTool {
	return functionDefinition("web_fetch", "Fetch a web page and return its title and readable text.", webFetchSchema)
}

func (t WebFetchTool) Run(ctx context.Context, raw json.RawMessage) Result {
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("invalid web_fetch arguments: %w", err)}
	}
	target, err := parseFetchURL(args.URL)
	if err != nil {
		return Result{Err: err}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Result{Err: fmt.Errorf("create web_fetch request: %w", err)}
	}
	request.Header.Set("User-Agent", webUserAgent)
	request.Header.Set("Accept", "text/html, text/plain, application/json;q=0.9, */*;q=0.1")

	response, err := t.client().Do(request)
	if err != nil {
		return Result{Err: fmt.Errorf("fetch %q: %w", target.String(), err)}
	}
	defer response.Body.Close()
	body, err := readWebBody(response.Body)
	if err != nil {
		return Result{Err: fmt.Errorf("read %q: %w", target.String(), err)}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Result{Err: fmt.Errorf("fetch %q returned %s", target.String(), response.Status)}
	}

	mediaType, err := responseMediaType(response.Header.Get("Content-Type"), body)
	if err != nil {
		return Result{Err: fmt.Errorf("read content type: %w", err)}
	}
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		title, text, err := parseHTMLPage(body)
		if err != nil {
			return Result{Err: fmt.Errorf("parse HTML page: %w", err)}
		}
		text = limitWebText(text)
		if title != "" {
			return Result{Output: limitWebText("Title: "+title) + "\n\n" + text}
		}
		return Result{Output: text}
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		return Result{Output: limitWebText(string(body))}
	default:
		return Result{Err: fmt.Errorf("unsupported content type %q; expected HTML, text, or JSON", mediaType)}
	}
}

func (t WebSearchTool) client() *http.Client {
	if t.httpClient != nil {
		return t.httpClient
	}
	return defaultWebHTTPClient
}

func (t WebFetchTool) client() *http.Client {
	if t.httpClient != nil {
		return t.httpClient
	}
	return defaultWebHTTPClient
}

type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func parseDuckDuckGoResults(body []byte, maxResults int) ([]webSearchResult, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	rows := make([]*html.Node, 0)
	collectElements(document, "tr", &rows)

	results := make([]webSearchResult, 0, maxResults)
	for i, row := range rows {
		anchor := findClassElement(row, "a", "result-link")
		if anchor == nil {
			continue
		}
		href := attribute(anchor, "href")
		resultURL, err := unwrapDuckDuckGoURL(href)
		if err != nil {
			continue
		}
		title := nodeText(anchor)
		if title == "" {
			continue
		}

		result := webSearchResult{Title: title, URL: resultURL}
		for j := i + 1; j < len(rows); j++ {
			if findClassElement(rows[j], "a", "result-link") != nil {
				break
			}
			if snippet := findClassElement(rows[j], "", "result-snippet"); snippet != nil {
				result.Snippet = nodeText(snippet)
				break
			}
		}
		results = append(results, result)
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}

func unwrapDuckDuckGoURL(href string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if (host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")) && strings.HasPrefix(parsed.Path, "/l/") {
		if target := parsed.Query().Get("uddg"); target != "" {
			parsed, err = url.Parse(target)
			if err != nil {
				return "", err
			}
		}
	}
	if !parsed.IsAbs() {
		base, _ := url.Parse("https://duckduckgo.com/")
		parsed = base.ResolveReference(parsed)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("result URL is not HTTP or HTTPS")
	}
	return parsed.String(), nil
}

func parseFetchURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("url must not be empty")
	}
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if !target.IsAbs() || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("url must be an absolute HTTP or HTTPS URL")
	}
	if target.User != nil {
		return nil, errors.New("url must not contain embedded credentials")
	}
	return target, nil
}

func parseHTMLPage(body []byte) (title, text string, err error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	if titleNode := findElement(document, "title"); titleNode != nil {
		title = nodeText(titleNode)
	}
	bodyNode := findElement(document, "body")
	if bodyNode == nil {
		bodyNode = document
	}
	text = nodeTextSkipping(bodyNode, map[string]bool{
		"script": true, "style": true, "noscript": true, "template": true,
	})
	return title, text, nil
}

func responseMediaType(header string, body []byte) (string, error) {
	if strings.TrimSpace(header) == "" {
		return strings.TrimSpace(strings.Split(http.DetectContentType(body), ";")[0]), nil
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return "", err
	}
	return strings.ToLower(mediaType), nil
}

func readWebBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxWebResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxWebResponse {
		return nil, fmt.Errorf("response exceeds %d bytes", maxWebResponse)
	}
	return data, nil
}

func limitWebText(text string) string {
	text = strings.ToValidUTF8(text, "�")
	if utf8.RuneCountInString(text) <= maxWebTextRunes {
		return text
	}
	var limited strings.Builder
	count := 0
	for _, r := range text {
		if count == maxWebTextRunes {
			break
		}
		limited.WriteRune(r)
		count++
	}
	return limited.String() + "\n[content truncated at 20000 characters]"
}

func collectElements(node *html.Node, tag string, matches *[]*html.Node) {
	if node.Type == html.ElementNode && node.Data == tag {
		*matches = append(*matches, node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectElements(child, tag, matches)
	}
}

func findElement(node *html.Node, tag string) *html.Node {
	if node.Type == html.ElementNode && node.Data == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if match := findElement(child, tag); match != nil {
			return match
		}
	}
	return nil
}

func findClassElement(node *html.Node, tag, class string) *html.Node {
	if node.Type == html.ElementNode && (tag == "" || node.Data == tag) && hasClass(node, class) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if match := findClassElement(child, tag, class); match != nil {
			return match
		}
	}
	return nil
}

func hasClass(node *html.Node, class string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" {
			for _, candidate := range strings.Fields(attr.Val) {
				if candidate == class {
					return true
				}
			}
		}
	}
	return false
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func nodeText(node *html.Node) string {
	return nodeTextSkipping(node, nil)
}

func nodeTextSkipping(node *html.Node, skip map[string]bool) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && skip[current.Data] {
			return
		}
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
			text.WriteByte(' ')
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(text.String()), " ")
}
