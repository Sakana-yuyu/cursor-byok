package safego

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGoWithPanicHandlerReportsRecoveredPanic(t *testing.T) {
	recovered := make(chan error, 1)
	GoWithPanicHandler("test:panic", func() {
		panic("boom")
	}, func(err error) {
		recovered <- err
	})

	select {
	case err := <-recovered:
		if err == nil || !strings.Contains(err.Error(), "test:panic") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("recovered error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("panic handler was not called")
	}
}

func TestGoWithPanicHandlerProtectsAgainstHandlerPanic(t *testing.T) {
	done := make(chan struct{})
	GoWithPanicHandler("test:handler-panic", func() {
		panic(errors.New("worker panic"))
	}, func(error) {
		defer close(done)
		panic("handler panic")
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("panic handler was not called")
	}
}
