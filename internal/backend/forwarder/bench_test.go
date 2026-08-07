// bench_test.go 承载 forwarder 事件分发与文本累积热点的性能基线基准：
// 1) 模拟 postStreamCommandWait 的每事件 make(chan error,1) + mailbox 往返模式；
// 2) 模拟 ProviderAccumulatedText/Reasoning 的 += 字符串累积（O(n²)）。
// 用于量化「每事件 channel 分配/往返」与「字符串拼接」开销，并验证后续优化效果。
package forwarder

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// benchStreamCommandEnvelopeActor 模拟 runStreamActor：从 mailbox 取命令，
// 处理（此处为空操作），若有 result channel 则回写结果。
func benchStreamCommandEnvelopeActor(mailbox <-chan streamCommandEnvelope, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case envelope := <-mailbox:
			if envelope.result != nil {
				envelope.result <- nil
			}
		}
	}
}

// BenchmarkStreamCommandWaitPerEvent 量化「每事件 make(chan) + mailbox 往返 + 等 actor 处理完」
// 的调度开销（优化前语义：每事件新建 result channel）。
func BenchmarkStreamCommandWaitPerEvent(b *testing.B) {
	mailbox := make(chan streamCommandEnvelope, 128)
	done := make(chan struct{})
	defer close(done)
	go benchStreamCommandEnvelopeActor(mailbox, done)

	command := streamCommand{Kind: streamCommandProviderEvent}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := make(chan error, 1)
		envelope := streamCommandEnvelope{command: command, result: result}
		mailbox <- envelope
		if err := <-result; err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkStreamCommandWaitPool 量化优化后语义：result channel 从 sync.Pool 复用，
// 与 postStreamCommandWait（actor.go）的 pool + drain 模式一致。
func BenchmarkStreamCommandWaitPool(b *testing.B) {
	mailbox := make(chan streamCommandEnvelope, 128)
	done := make(chan struct{})
	defer close(done)
	go benchStreamCommandEnvelopeActor(mailbox, done)

	command := streamCommand{Kind: streamCommandProviderEvent}
	pool := &sync.Pool{
		New: func() any { return make(chan error, 1) },
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := pool.Get().(chan error)
		select {
		case <-result:
		default:
		}
		envelope := streamCommandEnvelope{command: command, result: result}
		mailbox <- envelope
		select {
		case <-done:
			b.Fatal("unexpected done")
		case <-result:
		}
		select {
		case <-result:
		default:
		}
		pool.Put(result)
	}
}

// BenchmarkStreamCommandWaitAsyncNoWait 量化纯异步入队（postStreamCommandAsync）的成本，
// 作为对比基线：同样走 mailbox，但不等待 actor 回写。
func BenchmarkStreamCommandWaitAsyncNoWait(b *testing.B) {
	mailbox := make(chan streamCommandEnvelope, 1024)
	done := make(chan struct{})
	defer close(done)
	go benchStreamCommandEnvelopeActor(mailbox, done)

	command := streamCommand{Kind: streamCommandProviderEvent}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mailbox <- streamCommandEnvelope{command: command}
	}
}

// benchmarkTextDeltaChunks 生成模拟流式文本增量事件的一组文本块。
func benchmarkTextDeltaChunks(count int) []string {
	chunks := make([]string, 0, count)
	for i := 0; i < count; i++ {
		chunks = append(chunks, fmt.Sprintf("delta chunk number %d with some realistic tokenized content ", i))
	}
	return chunks
}

// BenchmarkTextAccumulationConcat 量化优化前 applyProviderModelEvent 中
// `stream.ProviderAccumulatedText += event.Text` 的每事件拼接开销（全回合 O(n²)）。
func BenchmarkTextAccumulationConcat(b *testing.B) {
	chunks := benchmarkTextDeltaChunks(2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		accumulated := ""
		for _, chunk := range chunks {
			accumulated += chunk
		}
		if len(accumulated) == 0 {
			b.Fatal("empty accumulation")
		}
	}
}

// BenchmarkTextAccumulationAppend 量化优化后 applyProviderModelEvent 中
// `stream.ProviderAccumulatedText = append(...)` 的每事件开销（全回合 O(n)）。
func BenchmarkTextAccumulationAppend(b *testing.B) {
	chunks := benchmarkTextDeltaChunks(2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		accumulated := []byte{}
		for _, chunk := range chunks {
			accumulated = append(accumulated, chunk...)
		}
		if len(accumulated) == 0 {
			b.Fatal("empty accumulation")
		}
	}
}

// BenchmarkTextAccumulationBuilder 量化 strings.Builder 等价实现，作为参考对比。
func BenchmarkTextAccumulationBuilder(b *testing.B) {
	chunks := benchmarkTextDeltaChunks(2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var builder strings.Builder
		builder.Grow(len(chunks) * 48)
		for _, chunk := range chunks {
			builder.WriteString(chunk)
		}
		if builder.Len() == 0 {
			b.Fatal("empty accumulation")
		}
	}
}
