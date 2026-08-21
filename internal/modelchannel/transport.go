package modelchannel

import (
	"fmt"
	"net/url"
	"strings"
)

type TransportPlanInput struct {
	Provider       string
	BaseURL        string
	ModelID        string
	ProtocolMode   string
	ProtocolGroup  string
	OpenAIEndpoint string
	Stream         bool
}

type ResolvedTransportPlan struct {
	Provider       string
	RequestURL     string
	ProtocolGroup  string
	OpenAIEndpoint string
}

func ResolveTransportPlan(input TransportPlanInput) (ResolvedTransportPlan, error) {
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	parsed, err := parseTransportURL(input.BaseURL)
	if err != nil {
		return ResolvedTransportPlan{}, err
	}
	plan := ResolvedTransportPlan{Provider: provider}
	switch provider {
	case "openai":
		group := ResolveProtocolGroup(input.ProtocolMode, provider, input.ModelID, input.BaseURL, input.OpenAIEndpoint, input.ProtocolGroup)
		if group == "" {
			return ResolvedTransportPlan{}, fmt.Errorf("openai protocol group is invalid")
		}
		endpoint := OpenAIEndpointForProtocolGroup(group, input.OpenAIEndpoint)
		if endpoint == "" {
			return ResolvedTransportPlan{}, fmt.Errorf("openai endpoint is invalid")
		}
		plan.ProtocolGroup = group
		plan.OpenAIEndpoint = endpoint
		if endpoint != OpenAIEndpointCustom {
			applyOpenAITransportPath(parsed, group)
		}
	case "anthropic":
		plan.ProtocolGroup = ProtocolGroupAnthropicMessages
		applyAnthropicTransportPath(parsed)
	case "gemini":
		plan.ProtocolGroup = ProtocolGroupGeminiNative
		if !geminiURLHasCompleteMethod(parsed.Path) {
			if strings.TrimSpace(input.ModelID) == "" {
				return ResolvedTransportPlan{}, fmt.Errorf("gemini model id is required")
			}
			applyGeminiTransportPath(parsed, input.ModelID, input.Stream)
		}
		if input.Stream {
			setRawQueryValue(parsed, "alt", "sse")
		}
	default:
		return ResolvedTransportPlan{}, fmt.Errorf("unsupported provider %q", provider)
	}
	plan.RequestURL = parsed.String()
	return plan, nil
}

func BaseURLWithoutKnownOpenAIEndpoint(raw string) string {
	parsed, err := parseTransportURL(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	path := strings.TrimRight(parsed.Path, "/")
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, "/chat/completions"):
		path = path[:len(path)-len("/chat/completions")]
	case strings.HasSuffix(lower, "/responses"):
		path = path[:len(path)-len("/responses")]
	}
	parsed.Path = cleanTransportPath(path)
	parsed.RawPath = ""
	return parsed.String()
}

func SameEffectiveOrigin(left, right string) bool {
	leftURL, leftErr := parseTransportURL(left)
	rightURL, rightErr := parseTransportURL(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		effectivePort(leftURL) == effectivePort(rightURL)
}

func URLPathForProtocol(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return parsed.Path
}

func URLHostForProtocol(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func parseTransportURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return nil, fmt.Errorf("transport URL is invalid")
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	parsed.Host = strings.ToLower(strings.TrimSpace(parsed.Host))
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("transport URL must use http or https")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("transport URL host is required")
	}
	parsed.Fragment = ""
	return parsed, nil
}

func effectivePort(parsed *url.URL) string {
	if port := parsed.Port(); port != "" {
		return port
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return "443"
	}
	return "80"
}

func applyOpenAITransportPath(parsed *url.URL, group string) {
	path := strings.TrimRight(parsed.Path, "/")
	lower := strings.ToLower(path)
	targetSuffix := "/chat/completions"
	defaultPath := OpenAIEndpointChatCompletions
	if group == ProtocolGroupResponses {
		targetSuffix = "/responses"
		defaultPath = OpenAIEndpointResponses
	}
	switch {
	case strings.HasSuffix(lower, targetSuffix):
		return
	case strings.HasSuffix(lower, "/chat/completions"):
		path = path[:len(path)-len("/chat/completions")] + targetSuffix
	case strings.HasSuffix(lower, "/responses"):
		path = path[:len(path)-len("/responses")] + targetSuffix
	case pathHasVersionSuffix(path):
		path += targetSuffix
	default:
		path += defaultPath
	}
	parsed.Path = cleanTransportPath(path)
	parsed.RawPath = ""
}

func applyAnthropicTransportPath(parsed *url.URL) {
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), "/messages") {
		return
	}
	if pathHasVersionSuffix(path) {
		path += "/messages"
	} else {
		path += "/v1/messages"
	}
	parsed.Path = cleanTransportPath(path)
	parsed.RawPath = ""
}

func applyGeminiTransportPath(parsed *url.URL, modelID string, stream bool) {
	method := "generateContent"
	if stream {
		method = "streamGenerateContent"
	}
	baseEscaped := strings.TrimRight(parsed.EscapedPath(), "/")
	rawPath := baseEscaped + "/models/" + url.PathEscape(strings.TrimSpace(modelID)) + ":" + method
	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		decoded = rawPath
	}
	parsed.Path = cleanTransportPath(decoded)
	parsed.RawPath = cleanTransportPath(rawPath)
}

func geminiURLHasCompleteMethod(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(lower, ":generatecontent") || strings.HasSuffix(lower, ":streamgeneratecontent")
}

func setRawQueryValue(parsed *url.URL, key, value string) {
	encodedKey := url.QueryEscape(key)
	encodedValue := url.QueryEscape(value)
	parts := strings.Split(parsed.RawQuery, "&")
	replaced := false
	output := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		if part == "" {
			continue
		}
		name := part
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		decodedName, err := url.QueryUnescape(name)
		if err == nil && decodedName == key {
			if !replaced {
				output = append(output, encodedKey+"="+encodedValue)
				replaced = true
			}
			continue
		}
		output = append(output, part)
	}
	if !replaced {
		output = append(output, encodedKey+"="+encodedValue)
	}
	parsed.RawQuery = strings.Join(output, "&")
}

func pathHasVersionSuffix(path string) bool {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	index := strings.LastIndex(path, "/")
	segment := path[index+1:]
	if len(segment) < 2 || (segment[0] != 'v' && segment[0] != 'V') {
		return false
	}
	for _, char := range segment[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func cleanTransportPath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}
