package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChainAppliesMiddlewareInOrder(t *testing.T) {
	var order []string
	m1 := func(next HandlerFunc) HandlerFunc {
		return func(ctx *Context) error {
			order = append(order, "m1")
			return next(ctx)
		}
	}
	m2 := func(next HandlerFunc) HandlerFunc {
		return func(ctx *Context) error {
			order = append(order, "m2")
			return next(ctx)
		}
	}
	handler := Chain(m1, m2)(func(ctx *Context) error {
		order = append(order, "handler")
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	ctx := newContext(rec, req, Route{Name: "test"})
	if err := handler(ctx); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	want := []string{"m1", "m2", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full=%v)", i, order[i], want[i], order)
		}
	}
}

func TestRegisteredRouteInvokesLocalHandler(t *testing.T) {
	called := false
	router := New(
		GET("/health", Local(func(ctx *Context) error {
			called = true
			ctx.Writer.WriteHeader(http.StatusOK)
			_, _ = ctx.Writer.Write([]byte(`{"ok":"true"}`))
			return nil
		})),
	)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected local handler to run")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
