package server

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cursor/internal/i18n"
)

const HeaderServerUpstreamURL = "X-Server-Upstream-URL"

type SourceKind string

const (
	SourceNative SourceKind = "native"
	SourceMITM   SourceKind = "mitm"
)

type ProtocolClass string

const (
	ProtocolHTTP          ProtocolClass = "http"
	ProtocolConnectUnary  ProtocolClass = "connect_unary"
	ProtocolConnectStream ProtocolClass = "connect_stream"
)

type Context struct {
	Writer    http.ResponseWriter
	Request   *http.Request
	RouteName string
	Source    SourceKind
	Protocol  ProtocolClass
	StartedAt time.Time

	Locale      string
	UpstreamURL *url.URL
	LastError   error

	Logger *slog.Logger
}

func newContext(writer http.ResponseWriter, request *http.Request, route Route) *Context {
	locale := i18n.DefaultLocale
	if request != nil {
		locale = i18n.Normalize(strings.Split(request.Header.Get("Accept-Language"), ",")[0])
	}
	return &Context{
		Writer:    writer,
		Request:   request,
		RouteName: route.Name,
		Protocol:  route.Protocol,
		StartedAt: time.Now(),
		Logger:    slog.Default(),
		Locale:    locale,
	}
}

func (ctx *Context) ParseUpstreamURL() error {
	if ctx == nil || ctx.Request == nil {
		return nil
	}
	rawURL := strings.TrimSpace(ctx.Request.Header.Get(HeaderServerUpstreamURL))
	if rawURL == "" {
		ctx.Source = SourceNative
		ctx.UpstreamURL = nil
		return nil
	}
	parsed, err := ParseAndValidateRawURL(rawURL)
	if err != nil {
		return err
	}
	ctx.Source = SourceMITM
	ctx.UpstreamURL = parsed
	return nil
}
