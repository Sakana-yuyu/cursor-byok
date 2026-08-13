package mitm

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"cursor/gen/aiserverv1"

	"google.golang.org/protobuf/proto"
)

// capturedGzipBidiAppendBodyBase64 是隔离抓包里一条真实的 gzip BidiAppend 请求体（389 字节压缩、
// 1078 字节解压）。取自一次性隔离实例的临时 scratch 工作区，内层只有 exec 结果里的 CI 配置文本，
// 不含用户数据与绝对路径；内联字节是为了让回归测试不依赖被 gitignore 的抓包目录。
const capturedGzipBidiAppendBodyBase64 = "H4sIAAAAAAAACpVTQY7cMAxDb8Uee+oDit4GkCmLtJ8Tx85b+pn+sXBmdiaZHXRbGAliySIlhn779TWhJMtW0ubLVs1t2Tjf" +
	"qWBQrMosCgKbxE1gI7lyk+TY6KwYquxcE3o252BiZ/gCy56rdVvmw43D9y8YTKaQs5wjXLnG5AglBUOufD2xI0wMyA81+Xhm" +
	"RvYoFBxy9CsnJvI1lhXoXJneK091rixcc5TqGXUu9pkVWFU+Zm9T7IpNZnSRIchZ3zuJBrhjeHIDsMLu+/S0BxD9uT+G7KjA" +
	"fXVcFY2ZhTFxnt55b/+pMOhs3BTK2UTP/4uyZ7JChn6cdEeLl3rcHPRarX9RDFlN7aw9xt+qYOrqr2Y7ebPOmZSZputmH3Na" +
	"NiaK8ej84TIYQ+XmEX857e6y3XHGqumYJxb0iQ1gROPEZXTgs15jx3108jn7QZ3J2e+xu+tTSYTNG9uKweLbz7cfgTVqz34p" +
	"NnDJpY5L8zYuNVSXseVYUL7//vIHXCTIAjYEAAA="

// recordMirrorRequestForTest 用保真记录器记录一次请求，并返回解析回来的那条记录。
func recordMirrorRequestForTest(t *testing.T, url string, body []byte, headers map[string]string) mirrorRecord {
	t.Helper()
	rec := newConfiguredMirrorRecorder(t.TempDir(), func() bool { return true })
	defer rec.Close()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	rec.recordRequest("api2.cursor.sh", req)

	raw, err := os.ReadFile(filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename))
	if err != nil {
		t.Fatalf("read mirror log: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 1 {
		t.Fatalf("got %d recorded lines, want 1: %s", len(lines), raw)
	}
	var record mirrorRecord
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("unmarshal mirror record: %v (%s)", err, lines[0])
	}
	return record
}

func recordGzipBidiAppendForTest(t *testing.T, body []byte, contentEncoding string) mirrorRecord {
	t.Helper()
	return recordMirrorRequestForTest(
		t,
		"https://api2.cursor.sh/aiserver.v1.BidiService/BidiAppend",
		body,
		map[string]string{"Content-Type": "application/proto", "Content-Encoding": contentEncoding},
	)
}

func gzipBytesForTest(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestMirrorBidiSummaryDecodesCapturedGzipBody 用抓包里的真实 gzip 帧固定主路径：
// 记录器必须自己解压后完成协议摘要，而不是标 unsupported_content_encoding。
func TestMirrorBidiSummaryDecodesCapturedGzipBody(t *testing.T) {
	body, err := base64.StdEncoding.DecodeString(capturedGzipBidiAppendBodyBase64)
	if err != nil {
		t.Fatal(err)
	}

	record := recordGzipBidiAppendForTest(t, body, "gzip")

	if record.Protocol == nil {
		t.Fatal("protocol summary missing")
	}
	if record.Protocol.DecodeError != "" {
		t.Fatalf("decodeError = %q, want empty (captured gzip frame must decode)", record.Protocol.DecodeError)
	}
	if record.Protocol.ClientMessageKind == "" {
		t.Fatal("clientMessageKind must be resolved from the decompressed body")
	}
	if len(record.Protocol.RequestIDHash) != 64 {
		t.Fatalf("requestIdHash = %q, want a sha256 hex digest", record.Protocol.RequestIDHash)
	}
	if record.Protocol.AppendSeqno == nil || *record.Protocol.AppendSeqno != 173 {
		t.Fatalf("appendSeqno = %v, want 173 (captured frame value)", record.Protocol.AppendSeqno)
	}
	if record.Protocol.ClientPayloadSource != "data" {
		t.Fatalf("clientPayloadSource = %q, want %q", record.Protocol.ClientPayloadSource, "data")
	}
	if record.BodyBase64 == nil || *record.BodyBase64 != capturedGzipBidiAppendBodyBase64 {
		t.Fatal("bodyBase64 must keep the original compressed bytes")
	}
}

// BenchmarkMirrorDecompressRequestBody 量化解压成本：镜像请求记录发生在 goproxy OnRequest
// 同步路径上，需要确认这一步相对转发是可忽略的。
func BenchmarkMirrorDecompressRequestBody(b *testing.B) {
	body, err := base64.StdEncoding.DecodeString(capturedGzipBidiAppendBodyBase64)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, err := mirrorDecompressRequestBody(body); err != nil {
			b.Fatal(err)
		}
	}
}

// TestMirrorBidiSummaryDecodesGzipContentEncodingList 固定 Content-Encoding 解析规则：
// 取值大小写不敏感，identity 是空操作可以从列表里剔除。
func TestMirrorBidiSummaryDecodesGzipContentEncodingList(t *testing.T) {
	appendRequest := &aiserverv1.BidiAppendRequest{
		RequestId:   &aiserverv1.BidiRequestId{RequestId: "list-encoding-request"},
		AppendSeqno: 7,
	}
	payload, err := proto.Marshal(appendRequest)
	if err != nil {
		t.Fatal(err)
	}
	body := gzipBytesForTest(t, payload)

	for _, encoding := range []string{"GZIP", "gzip, identity", "identity, GZip"} {
		record := recordGzipBidiAppendForTest(t, body, encoding)
		if record.Protocol == nil {
			t.Fatalf("content-encoding %q: protocol summary missing", encoding)
		}
		if record.Protocol.DecodeError != "" {
			t.Fatalf("content-encoding %q: decodeError = %q, want empty", encoding, record.Protocol.DecodeError)
		}
		if record.Protocol.AppendSeqno == nil || *record.Protocol.AppendSeqno != 7 {
			t.Fatalf("content-encoding %q: appendSeqno = %v, want 7", encoding, record.Protocol.AppendSeqno)
		}
	}
}

// TestMirrorBidiSummaryMarksOversizedGzipBody 固定解压炸弹防护：解压输出超过上限时
// 必须降级成独立的超限标记（区别于 unsupported_content_encoding），并保留原始字节。
func TestMirrorBidiSummaryMarksOversizedGzipBody(t *testing.T) {
	body := gzipBytesForTest(t, make([]byte, mirrorDecompressedBodyMaxBytes+1))
	if len(body) > mirrorBodyMaxBytes {
		t.Fatalf("compressed bomb is %d bytes, must stay under the record body cap", len(body))
	}

	record := recordGzipBidiAppendForTest(t, body, "gzip")

	if record.Protocol == nil {
		t.Fatal("protocol summary missing")
	}
	if record.Protocol.DecodeError != "content_encoding_gzip_too_large" {
		t.Fatalf("decodeError = %q, want %q", record.Protocol.DecodeError, "content_encoding_gzip_too_large")
	}
	if record.BodyBase64 == nil || *record.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
		t.Fatal("bodyBase64 must keep the original compressed bytes on over-limit degrade")
	}
}

// TestMirrorBidiSummaryKeepsBodyOnCorruptGzip 固定失败降级：gzip 流损坏时只标记解码失败，
// 绝不丢掉 bodyBase64（外部工具仍能拿到完整原始字节）。
func TestMirrorBidiSummaryKeepsBodyOnCorruptGzip(t *testing.T) {
	complete := gzipBytesForTest(t, bytes.Repeat([]byte("payload"), 512))
	truncated := complete[:len(complete)/2]

	record := recordGzipBidiAppendForTest(t, truncated, "gzip")

	if record.Protocol == nil {
		t.Fatal("protocol summary missing")
	}
	if record.Protocol.DecodeError != "content_encoding_gzip_failed" {
		t.Fatalf("decodeError = %q, want %q", record.Protocol.DecodeError, "content_encoding_gzip_failed")
	}
	if record.BodyBase64 == nil || *record.BodyBase64 != base64.StdEncoding.EncodeToString(truncated) {
		t.Fatal("bodyBase64 must keep the original compressed bytes on decode failure")
	}
}

// TestMirrorBidiSummaryRejectsUnsupportedContentEncoding 保持未支持编码的既有降级语义：
// 抓包里从未出现 br / deflate，记录器不猜测这些编码，只标记并保留原始字节。
func TestMirrorBidiSummaryRejectsUnsupportedContentEncoding(t *testing.T) {
	for _, encoding := range []string{"br", "deflate", "gzip, br"} {
		record := recordGzipBidiAppendForTest(t, []byte("not-decodable"), encoding)
		if record.Protocol == nil {
			t.Fatalf("content-encoding %q: protocol summary missing", encoding)
		}
		if record.Protocol.DecodeError != "unsupported_content_encoding" {
			t.Fatalf("content-encoding %q: decodeError = %q, want %q", encoding, record.Protocol.DecodeError, "unsupported_content_encoding")
		}
		if record.BodyBase64 == nil {
			t.Fatalf("content-encoding %q: bodyBase64 must be preserved", encoding)
		}
	}
}

// TestMirrorRunSSESummaryDecodesGzipRequestBody 让 RunSSE 请求走同一套 content-encoding 处理：
// 本次抓包里 RunSSE 请求没被 gzip 过，但两条摘要路径共用同一个判定入口，行为必须一致。
func TestMirrorRunSSESummaryDecodesGzipRequestBody(t *testing.T) {
	requestID, err := proto.Marshal(&aiserverv1.BidiRequestId{RequestId: "runsse-request"})
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 5, 5+len(requestID))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(requestID)))
	frame = append(frame, requestID...)

	record := recordMirrorRequestForTest(
		t,
		"https://api2.cursor.sh/agent.v1.AgentService/RunSSE",
		gzipBytesForTest(t, frame),
		map[string]string{"Content-Type": "application/connect+proto", "Content-Encoding": "gzip"},
	)

	if record.Protocol == nil {
		t.Fatal("protocol summary missing")
	}
	if record.Protocol.DecodeError != "" {
		t.Fatalf("decodeError = %q, want empty", record.Protocol.DecodeError)
	}
	if len(record.Protocol.RequestIDHash) != 64 {
		t.Fatalf("requestIdHash = %q, want a sha256 hex digest", record.Protocol.RequestIDHash)
	}
}
