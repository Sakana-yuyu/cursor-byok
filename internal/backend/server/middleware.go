package server

import (
	"cursor/internal/i18n"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	legacyruntime "cursor/internal/runtime"
)

func Recover() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx *Context) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("panic: %v\n%s", recovered, string(debug.Stack()))
				}
			}()
			return next(ctx)
		}
	}
}

func ServerContext() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx *Context) error {
			if ctx == nil {
				return fmt.Errorf("server context is nil")
			}
			if err := ctx.ParseUpstreamURL(); err != nil {
				return err
			}
			return next(ctx)
		}
	}
}

func ErrorEncoder() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx *Context) error {
			if ctx != nil {
				ctx.LastError = nil
			}
			if err := next(ctx); err != nil {
				if ctx != nil {
					ctx.LastError = err
				}
				if ctx == nil || ctx.Writer == nil {
					return err
				}
				writeServerError(ctx.Writer, err, ctx.Locale)
				return nil
			}
			return nil
		}
	}
}

func writeServerError(writer http.ResponseWriter, err error, locale ...string) {
	if responseWriterHasWrittenHeader(writer) {
		return
	}
	selectedLocale := i18n.DefaultLocale
	if len(locale) > 0 {
		selectedLocale = i18n.Normalize(locale[0])
	}
	status := http.StatusBadGateway
	message := i18n.Display(selectedLocale, err)
	switch {
	case err == nil:
		status = http.StatusOK
		message = ""
	case strings.TrimSpace(err.Error()) == "empty raw url":
		status = http.StatusBadRequest
		message = i18n.T(selectedLocale, "error.request.invalid")
	case errors.Is(err, ErrInvalidBidiAppendPayload):
		status = http.StatusBadRequest
		message = i18n.T(selectedLocale, "error.request.invalid")
	case errors.Is(err, legacyruntime.ErrInvalidSystemSetting):
		status = http.StatusInternalServerError
		message = i18n.T(selectedLocale, "error.backend.failed")
	case errors.Is(err, legacyruntime.ErrChannelNotAvailable):
		status = http.StatusServiceUnavailable
		message = i18n.T(selectedLocale, "error.provider.failed")
	}
	var userErr *i18n.UserError
	if errors.As(err, &userErr) {
		switch userErr.Code() {
		case i18n.CodeInvalidModelAdapter, i18n.CodeModelCatalog, i18n.CodeInvalidRequest:
			status = http.StatusBadRequest
		case i18n.CodeCertificate, i18n.CodeUpdate, i18n.CodeBackend, i18n.CodeProxy:
			status = http.StatusInternalServerError
		case i18n.CodeProvider:
			status = http.StatusBadGateway
		}
	}
	http.Error(writer, message, status)
}
