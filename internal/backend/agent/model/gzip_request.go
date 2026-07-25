// gzip_request.go 提供 HTTP 请求体的 gzip 压缩与 413 自动降级重试。
package modeladapter

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
)

// gzipRequestPayload 用 gzip 压缩请求体。
func gzipRequestPayload(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(payload); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// doProviderRequestWithGzipFallback 先用原始请求体发送；
// 收到 413 时自动用 gzip 压缩请求体重试一次。
// configureHeaders 负责设置 Authorization、Content-Type 等自定义头。
func doProviderRequestWithGzipFallback(
	ctx context.Context,
	client *http.Client,
	provider string,
	requestID string,
	modelCallID string,
	payload []byte,
	requestURL string,
	configureHeaders func(*http.Request) error,
) (*http.Response, error) {
	buildRequest := func(_ context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		if err := configureHeaders(httpReq); err != nil {
			return nil, err
		}
		return httpReq, nil
	}

	resp, err := doProviderRequestWithRetry(ctx, client, provider, requestID, modelCallID, buildRequest)
	if err != nil {
		return nil, err
	}

	// 413 → gzip 压缩后重试一次
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		resp.Body.Close()

		compressed, gzErr := gzipRequestPayload(payload)
		if gzErr != nil {
			return nil, gzErr
		}

		buildGzipRequest := func(_ context.Context) (*http.Request, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(compressed))
			if err != nil {
				return nil, err
			}
			httpReq.Header.Set("Content-Encoding", "gzip")
			if err := configureHeaders(httpReq); err != nil {
				return nil, err
			}
			return httpReq, nil
		}

		resp, err = doProviderRequestWithRetry(ctx, client, provider, requestID, modelCallID, buildGzipRequest)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}
