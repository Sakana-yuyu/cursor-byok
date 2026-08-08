package upstream

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cursor/internal/backend/server"
	legacyruntime "cursor/internal/runtime"
)

const (
	HeaderRawServerURL = server.HeaderServerUpstreamURL
)

type SystemSettingService interface {
	ResolveModelAdapters(context.Context) ([]legacyruntime.ModelAdapterConfig, error)
}

// AuthorizationProvider supplies the independent Cursor account used only by
// official control-plane requests such as Plugins, Skills, and MCP registry.
type AuthorizationProvider interface {
	Authorization(context.Context) (string, error)
	SignedIn() bool
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Dependencies struct {
	SystemSettingService SystemSettingService
	HTTPClient           HTTPClient
	LogRoot              string
	Routes               []Route
}

type RequestContext struct {
	ResponseWriter http.ResponseWriter
	Request        *http.Request
	StartedAt      time.Time
	RawURL         string
	TargetURL      *url.URL
	Method         string
	Headers        http.Header
	ContentType    string
	RequestBody    []byte
	Deps           *Dependencies
	HTTPRequestID  string
}

type ForwardOptions struct {
	BodyOverride []byte
	PatchHeaders func(headers http.Header)
}

type ForwardMeta struct {
	StatusCode   int
	Status       string
	ContentType  string
	ResponseSize int64
}

type Matcher interface {
	Match(path string) bool
}

type Exact string

func (m Exact) Match(path string) bool { return path == string(m) }

type Prefix string

func (m Prefix) Match(path string) bool {
	value := string(m)
	return value != "" && strings.HasPrefix(path, value)
}

type Wildcard struct{}

func (Wildcard) Match(string) bool { return true }

type RouteHandler func(reqCtx *RequestContext, route *Route) error

type Route struct {
	Name               string
	Pattern            string
	Matcher            Matcher
	ConsoleLog         bool
	StatusCode         int
	JSONBody           map[string]any
	MockProtoType      string
	MockPayloadBuilder func(*RequestContext) (map[string]any, error)
	Handler            RouteHandler
}
