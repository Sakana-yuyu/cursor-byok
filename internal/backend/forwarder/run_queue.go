// run_queue.go 实现「子代理运行期间新消息排队不中断」机制。
//
// 当某会话存在非终态且含运行中子代理（PendingExecs 中 ExecKind=="subagent"）的 stream 时，
// 新到达的 run intent 不再取消旧 stream（避免杀死子代理），而是进入按会话维度的队列；
// 等当前 turn 到达终态后，自动取出队列里的 intent 新建 stream 跑一次全新 handleRunIntent。
// 无子代理时维持原有「新消息取代旧 run」行为不变。
package forwarder

import (
	"log"
	"strings"
	"sync"
)

// runQueue 是按会话维度的 run intent 队列，FIFO。同一会话可排队多条，
// 每次终态排空一条，下一条会在新 turn 终态时再触发（递归串行）。
type runQueue struct {
	mu     sync.Mutex
	queues map[string][]InboundIntent
}

func newRunQueue() *runQueue {
	return &runQueue{queues: make(map[string][]InboundIntent)}
}

// Enqueue 把一条 run intent 追加到指定会话的队列尾部。
func (q *runQueue) Enqueue(conversationID string, intent InboundIntent) {
	if q == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queues[conversationID] = append(q.queues[conversationID], intent)
}

// Dequeue 取出指定会话队列里最早的一条（FIFO）；队列空返回 false。
func (q *runQueue) Dequeue(conversationID string) (InboundIntent, bool) {
	if q == nil {
		return InboundIntent{}, false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return InboundIntent{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.queues[conversationID]
	if len(items) == 0 {
		return InboundIntent{}, false
	}
	intent := items[0]
	items[0] = InboundIntent{} // 帮助 GC
	q.queues[conversationID] = items[1:]
	if len(q.queues[conversationID]) == 0 {
		delete(q.queues, conversationID)
	}
	return intent, true
}

// Len 返回指定会话队列里剩余的排队条数。
func (q *runQueue) Len(conversationID string) int {
	if q == nil {
		return 0
	}
	conversationID = strings.TrimSpace(conversationID)
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queues[conversationID])
}

// activeConversationHasSubagents 判断指定会话是否存在非终态且含运行中子代理的 stream。
// 复用 broker.OtherConversationRequestIDs（它已过滤终态 stream）；对每个返回的非终态 stream，
// 检查其 PendingExecs 中是否存在 ExecKind=="subagent"。
func (service *Service) activeConversationHasSubagents(conversationID string) bool {
	if service == nil || service.broker == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false
	}
	for _, requestID := range service.broker.OtherConversationRequestIDs(conversationID, "") {
		stream, ok := service.broker.Get(requestID)
		if !ok || stream == nil {
			continue
		}
		stream.mu.Lock()
		hit := false
		for _, pending := range stream.PendingExecs {
			if strings.TrimSpace(pending.ExecKind) == "subagent" {
				hit = true
				break
			}
		}
		stream.mu.Unlock()
		if hit {
			return true
		}
	}
	return false
}

// drainRunQueue 在某会话的当前 turn 到达终态后调用：取出最早一条排队的 run intent，
// 新建 stream 跑一次全新 handleRunIntent。只取一条，下一条会在新 turn 终态时再触发（递归串行）。
// 失败保护：若 handleRunIntent 出错，记日志后继续排空下一条，避免队列卡死。
func (service *Service) drainRunQueue(conversationID string) {
	if service == nil || service.runQueue == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	intent, ok := service.runQueue.Dequeue(conversationID)
	if !ok {
		return
	}
	log.Printf("forwarder run queue drained request_id=%s conversation_id=%s",
		strings.TrimSpace(intent.RequestID), conversationID)
	if err := service.handleRunIntent(intent); err != nil {
		log.Printf("forwarder run queue dispatch failed request_id=%s conversation_id=%s err=%v",
			strings.TrimSpace(intent.RequestID), conversationID, err)
		// 该条失败不卡住队列：递归排空下一条。
		service.drainRunQueue(conversationID)
	}
}
