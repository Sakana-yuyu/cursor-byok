// interaction_web.go 承载 web 执行域：搜索/抓取实现、HTML 转 Markdown、结果截断与正则/常量。
package interaction

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	htmlmarkdown "github.com/firecrawl/html-to-markdown"
	mdplugin "github.com/firecrawl/html-to-markdown/plugin"

	"cursor/gen/agentv1"
	"cursor/internal/netproxy"
	"google.golang.org/protobuf/proto"
)

var (
	webSearchAnchorPattern  = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	webSearchSnippetPattern = regexp.MustCompile(`(?is)<(?:a|div)[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</(?:a|div)>`)
	htmlTitlePattern        = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlTagPattern          = regexp.MustCompile(`(?is)<[^>]+>`)
	webSearchURLOverride    = "https://html.duckduckgo.com/html/?q="
)

const (
	webFetchBodyLimit     = 2 * 1024 * 1024
	webFetchMarkdownLimit = 32 * 1024
	webSearchPayloadLimit = 16 * 1024
	webSearchTitleLimit   = 512
	webSearchChunkLimit   = 2 * 1024
)

func (bridge *Bridge) executeWebSearch(searchTerm string) ([]*agentv1.WebSearchReference, string, error) {
	searchTerm = strings.TrimSpace(searchTerm)
	if searchTerm == "" {
		return nil, "", fmt.Errorf("web search search_term is required")
	}
	client := bridge.httpClient
	if client == nil {
		client = netproxy.NewHTTPClient(15 * time.Second)
	}

	// 先尝试百度搜索
	baiduReferences, baiduPayload, baiduErr := bridge.tryBaiduWebSearch(client, searchTerm)
	if baiduErr == nil && len(baiduReferences) > 0 {
		return baiduReferences, baiduPayload, nil
	}

	// 百度失败，回退到 DuckDuckGo
	duckReferences, duckPayload, duckErr := bridge.tryDuckDuckGoWebSearch(client, searchTerm)
	if duckErr == nil && len(duckReferences) > 0 {
		return duckReferences, duckPayload, nil
	}

	// 两者都失败，返回综合错误
	if baiduErr != nil && duckErr != nil {
		return nil, "", fmt.Errorf("web search failed: baidu=%v, duckduckgo=%v", baiduErr, duckErr)
	}
	return nil, "", fmt.Errorf("web search returned no parseable results")
}

func (bridge *Bridge) tryBaiduWebSearch(client *http.Client, searchTerm string) ([]*agentv1.WebSearchReference, string, error) {
	requestURL := baiduWebSearchBaseURL + neturl.QueryEscape(searchTerm)
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/68.0.3440.106 Safari/537.36")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	request.Header.Set("Referer", baiduWebSearchHostURL+"/")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("baidu http status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return nil, "", err
	}
	references := extractBaiduWebSearchReferences(string(body))
	if len(references) == 0 {
		return nil, "", fmt.Errorf("baidu returned no parseable results")
	}
	if len(references) > 5 {
		references = references[:5]
	}
	resolveBaiduWebSearchRedirects(client, references)
	return references, formatWebSearchPayload(searchTerm, references), nil
}

func (bridge *Bridge) tryDuckDuckGoWebSearch(client *http.Client, searchTerm string) ([]*agentv1.WebSearchReference, string, error) {
	requestURL := webSearchURLOverride + neturl.QueryEscape(searchTerm)
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", "cursor-local-agent/1.0")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("web search http status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return nil, "", err
	}
	references := extractWebSearchReferences(string(body))
	if len(references) == 0 {
		return nil, "", fmt.Errorf("web search returned no parseable results")
	}
	if len(references) > 5 {
		references = references[:5]
	}
	return references, formatWebSearchPayload(searchTerm, references), nil
}

func extractWebSearchReferences(body string) []*agentv1.WebSearchReference {
	anchorMatches := webSearchAnchorPattern.FindAllStringSubmatch(body, 8)
	snippetMatches := webSearchSnippetPattern.FindAllStringSubmatch(body, 8)
	references := make([]*agentv1.WebSearchReference, 0, len(anchorMatches))
	for index, match := range anchorMatches {
		if len(match) < 3 {
			continue
		}
		title := cleanupWebSearchHTML(match[2])
		url := strings.TrimSpace(html.UnescapeString(match[1]))
		snippet := ""
		if index < len(snippetMatches) && len(snippetMatches[index]) >= 2 {
			snippet = cleanupWebSearchHTML(snippetMatches[index][1])
		}
		if title == "" || url == "" {
			continue
		}
		references = append(references, &agentv1.WebSearchReference{
			Title: title,
			Url:   url,
			Chunk: snippet,
		})
	}
	return references
}

func cleanupWebSearchHTML(value string) string {
	withoutTags := htmlTagPattern.ReplaceAllString(value, " ")
	unescaped := html.UnescapeString(withoutTags)
	return strings.Join(strings.Fields(unescaped), " ")
}

func formatWebSearchPayload(searchTerm string, references []*agentv1.WebSearchReference) string {
	lines := []string{
		fmt.Sprintf("Title: Web search results for query: %s", strings.TrimSpace(searchTerm)),
		"Content: Links:",
	}
	for index, reference := range references {
		if reference == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. [%s](%s)", index+1, strings.TrimSpace(reference.GetTitle()), strings.TrimSpace(reference.GetUrl())))
	}
	snippets := make([]string, 0, len(references))
	for _, reference := range references {
		if reference == nil {
			continue
		}
		chunk := strings.TrimSpace(reference.GetChunk())
		if chunk == "" {
			continue
		}
		snippets = append(snippets, fmt.Sprintf("- %s", chunk))
	}
	if len(snippets) > 0 {
		lines = append(lines, "", strings.Join(snippets, "\n"))
	}
	return strings.Join(lines, "\n")
}

func truncateWebSearchReplay(searchTerm string, references []*agentv1.WebSearchReference, payload string) ([]*agentv1.WebSearchReference, string) {
	truncated := false
	nextReferences := make([]*agentv1.WebSearchReference, 0, len(references))
	for _, reference := range references {
		if reference == nil {
			continue
		}
		next, _ := proto.Clone(reference).(*agentv1.WebSearchReference)
		if next == nil {
			continue
		}
		title := truncateInteractionText("WebSearch title", next.GetTitle(), webSearchTitleLimit)
		chunk := truncateInteractionText("WebSearch snippet", next.GetChunk(), webSearchChunkLimit)
		if title != next.GetTitle() || chunk != next.GetChunk() {
			truncated = true
		}
		next.Title = title
		next.Chunk = chunk
		nextReferences = append(nextReferences, next)
	}
	nextPayload := formatWebSearchPayload(searchTerm, nextReferences)
	if strings.TrimSpace(payload) != "" && len(nextPayload) == 0 {
		nextPayload = payload
	}
	if len(nextPayload) > webSearchPayloadLimit {
		truncated = true
		nextPayload = truncateInteractionText("WebSearch", nextPayload, webSearchPayloadLimit)
	}
	if truncated && len(nextReferences) > 0 {
		last := nextReferences[len(nextReferences)-1]
		last.Chunk = strings.TrimSpace(last.GetChunk() + "\n\n" + interactionTruncationNotice("WebSearch", webSearchPayloadLimit, len(nextPayload), len(payload)))
		nextPayload = formatWebSearchPayload(searchTerm, nextReferences)
		nextPayload = truncateInteractionText("WebSearch", nextPayload, webSearchPayloadLimit)
	}
	return nextReferences, nextPayload
}

func (bridge *Bridge) executeWebFetch(rawURL string) (string, error) {
	parsedURL, err := validateWebFetchURL(rawURL)
	if err != nil {
		return "", err
	}
	client := bridge.httpClient
	if client == nil {
		client = netproxy.NewHTTPClient(15 * time.Second)
	}
	client = webFetchHTTPClient(client)
	request, err := http.NewRequest(http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "cursor-local-agent/1.0")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/xml,application/json;q=0.9,*/*;q=0.1")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("web fetch http status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, webFetchBodyLimit+1))
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", fmt.Errorf("web fetch returned empty body")
	}
	if len(body) > webFetchBodyLimit {
		body = body[:webFetchBodyLimit]
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	if !isWebFetchTextContentType(contentType) {
		return "", fmt.Errorf("web fetch unsupported content type %q", contentType)
	}
	markdown, title, err := renderWebFetchMarkdown(parsedURL, body, contentType)
	if err != nil {
		return "", err
	}
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return "", fmt.Errorf("web fetch returned empty markdown")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = parsedURL.String()
	}
	payload := fmt.Sprintf("Title: %s\nURL: %s\n\nContent:\n%s", title, parsedURL.String(), markdown)
	return truncateWebFetchMarkdown(payload), nil
}

func validateWebFetchURL(rawURL string) (*neturl.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("web fetch url is required")
	}
	parsedURL, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("web fetch invalid url: %w", err)
	}
	switch strings.ToLower(parsedURL.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("web fetch only supports http and https urls")
	}
	host := strings.TrimSpace(parsedURL.Hostname())
	if host == "" {
		return nil, fmt.Errorf("web fetch url host is required")
	}
	if isBlockedWebFetchHost(host) {
		return nil, fmt.Errorf("web fetch host is not public-web accessible")
	}
	return parsedURL, nil
}

func isBlockedWebFetchHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

func isWebFetchTextContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/xhtml+xml", "application/xml", "application/json", "application/ld+json", "application/rss+xml", "application/atom+xml":
		return true
	default:
		return strings.HasSuffix(mediaType, "+xml") || strings.HasSuffix(mediaType, "+json")
	}
}

func renderWebFetchMarkdown(pageURL *neturl.URL, body []byte, contentType string) (string, string, error) {
	if !isHTMLLikeContentType(contentType) {
		return string(body), "", nil
	}
	article, err := readability.FromReader(bytes.NewReader(body), pageURL)
	if err == nil {
		var articleHTML bytes.Buffer
		if renderErr := article.RenderHTML(&articleHTML); renderErr == nil && strings.TrimSpace(articleHTML.String()) != "" {
			if markdown, convertErr := convertHTMLToMarkdown(pageURL, articleHTML.String()); convertErr == nil && strings.TrimSpace(markdown) != "" {
				return markdown, article.Title(), nil
			}
		}
	}
	markdown, err := convertHTMLToMarkdown(pageURL, string(body))
	if err != nil {
		return "", "", fmt.Errorf("web fetch markdown conversion failed: %w", err)
	}
	return markdown, extractWebFetchHTMLTitle(string(body)), nil
}

func isHTMLLikeContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml" || mediaType == ""
}

func convertHTMLToMarkdown(pageURL *neturl.URL, htmlBody string) (string, error) {
	converter := htmlmarkdown.NewConverter(htmlmarkdown.DomainFromURL(pageURL.String()), true, nil)
	converter.Use(mdplugin.GitHubFlavored())
	return converter.ConvertString(htmlBody)
}

func extractWebFetchHTMLTitle(htmlBody string) string {
	matches := htmlTitlePattern.FindStringSubmatch(htmlBody)
	if len(matches) < 2 {
		return ""
	}
	return cleanupWebSearchHTML(matches[1])
}

func truncateWebFetchMarkdown(markdown string) string {
	return truncateInteractionText("WebFetch", markdown, webFetchMarkdownLimit)
}

func truncateInteractionText(toolName string, text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	original := len(text)
	notice := fmt.Sprintf("\n\n%s", interactionTruncationNotice(toolName, limit, limit, original))
	for {
		keep := limit - len(notice)
		if keep <= 0 {
			return truncateInteractionUTF8(text, limit)
		}
		kept := truncateInteractionUTF8(text, keep)
		nextNotice := fmt.Sprintf("\n\n%s", interactionTruncationNotice(toolName, limit, len(kept), original))
		output := strings.TrimRight(kept, "\n") + nextNotice
		if len(output) <= limit || nextNotice == notice {
			return output
		}
		notice = nextNotice
	}
}

func interactionTruncationNotice(toolName string, limit int, kept int, original int) string {
	return fmt.Sprintf("[truncated: %s result exceeded %d bytes; showing %d of %d bytes]", toolName, limit, kept, original)
}

func truncateInteractionUTF8(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if limit > len(text) {
		limit = len(text)
	}
	truncated := text[:limit]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func webFetchHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = netproxy.NewHTTPClient(15 * time.Second)
	}
	client := *base
	previousCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("web fetch stopped after 10 redirects")
		}
		if _, err := validateWebFetchURL(request.URL.String()); err != nil {
			return err
		}
		if previousCheckRedirect != nil {
			return previousCheckRedirect(request, via)
		}
		return nil
	}
	return &client
}
