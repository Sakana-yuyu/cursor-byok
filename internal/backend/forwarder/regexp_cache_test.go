package forwarder

import (
	"regexp"
	"testing"
)

func TestCachedRegexpCompileReusesCompiledPattern(t *testing.T) {
	pattern := `(?m)hello-\d+`
	first, err := cachedRegexpCompile(pattern)
	if err != nil {
		t.Fatalf("cachedRegexpCompile() error = %v", err)
	}
	second, err := cachedRegexpCompile(pattern)
	if err != nil {
		t.Fatalf("cachedRegexpCompile() second call error = %v", err)
	}
	if first != second {
		t.Fatal("expected cached regexp pointer to be reused")
	}
	if !first.MatchString("hello-42") {
		t.Fatalf("pattern %q did not match expected input", pattern)
	}
}

func TestCachedRegexpCompileInvalidPattern(t *testing.T) {
	_, err := cachedRegexpCompile(`(?m)[`)
	if err == nil {
		t.Fatal("expected invalid pattern error")
	}
	if _, ok := compiledRegexpCache.Load(`(?m)[`); ok {
		t.Fatal("invalid patterns must not be cached")
	}
}

func BenchmarkCachedRegexpCompileHit(b *testing.B) {
	pattern := `(?i)` + regexp.QuoteMeta("secret-token-value")
	if _, err := cachedRegexpCompile(pattern); err != nil {
		b.Fatalf("warm cache: %v", err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := cachedRegexpCompile(pattern); err != nil {
			b.Fatalf("cachedRegexpCompile() error = %v", err)
		}
	}
}
