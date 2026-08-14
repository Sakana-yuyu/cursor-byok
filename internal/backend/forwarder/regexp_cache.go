package forwarder

import (
	"regexp"
	"sync"
)

var compiledRegexpCache sync.Map // map[string]*regexp.Regexp

func cachedRegexpCompile(pattern string) (*regexp.Regexp, error) {
	if cached, ok := compiledRegexpCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := compiledRegexpCache.LoadOrStore(pattern, compiled)
	return actual.(*regexp.Regexp), nil
}
