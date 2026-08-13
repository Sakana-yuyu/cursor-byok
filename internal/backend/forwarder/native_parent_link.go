// native_parent_link.go 把 Cursor 客户端在子代理 RunSSE 上发送的父链路 HTTP 头还原成
// 后端可用的父子关系。native 子代理运行在**独立 conversation** 里，run_request 本身不带
// 任何父信息，父链路只存在于传输层头部：
//
//	x-parent-request-id          父 turn 的 generationUUID（= nativeDelegationRuntime.ParentRequestID）
//	x-root-parent-request-id     根 turn 的 generationUUID
//	x-parent-agent-tool-call-id  父侧 Task tool_call 的 id（= nativeDelegationRuntime.ToolCallID）
//
// 之前后端从不读取这些头，导致子会话的 parent_conversation_id / parent_tool_call_id 恒空，
// mirrorNativeChildInteraction 因此永远提前返回，父 Task 气泡只剩 12 秒一次的 keepAlive 摘要。
package forwarder

import (
	"net/http"
	"strings"
	"time"
)

const (
	headerParentRequestID       = "x-parent-request-id"
	headerRootParentRequestID   = "x-root-parent-request-id"
	headerParentAgentToolCallID = "x-parent-agent-tool-call-id"
)

// childParentLinkRetention 限制父链路记录的存活时间。子代理最长运行时间由
// nativeDelegationProgressTimeout 与 exec 看门狗共同约束，这里留足冗余即可。
const childParentLinkRetention = 2 * time.Hour

type childParentLink struct {
	ParentRequestID     string
	RootParentRequestID string
	ParentToolCallID    string
	RecordedAt          time.Time
}

func (link childParentLink) empty() bool {
	return strings.TrimSpace(link.ParentRequestID) == "" && strings.TrimSpace(link.ParentToolCallID) == ""
}

func parseChildParentLink(header http.Header) childParentLink {
	if header == nil {
		return childParentLink{}
	}
	return childParentLink{
		ParentRequestID:     strings.TrimSpace(header.Get(headerParentRequestID)),
		RootParentRequestID: strings.TrimSpace(header.Get(headerRootParentRequestID)),
		ParentToolCallID:    strings.TrimSpace(header.Get(headerParentAgentToolCallID)),
		RecordedAt:          time.Now().UTC(),
	}
}

// rememberChildParentLink 记录一次子代理 RunSSE 的父链路头。RunSSE 与 BidiAppend 的
// 到达顺序不固定（broker.Subscribe 就为「RunSSE 先到」留了占位流），所以这里在记录之后
// 还要对已经建好的子会话做一次补写，两种顺序都能落到 parent_conversation_id。
func (service *Service) rememberChildParentLink(requestID string, header http.Header) {
	if service == nil {
		return
	}
	requestID = strings.TrimSpace(requestID)
	link := parseChildParentLink(header)
	if requestID == "" || link.empty() {
		return
	}
	service.childParentLinkMu.Lock()
	if service.childParentLinks == nil {
		service.childParentLinks = make(map[string]childParentLink)
	}
	service.pruneChildParentLinksLocked(time.Now().UTC())
	service.childParentLinks[requestID] = link
	service.childParentLinkMu.Unlock()
	service.backfillChildParentLink(requestID, link)
}

func (service *Service) pruneChildParentLinksLocked(now time.Time) {
	cutoff := now.Add(-childParentLinkRetention)
	for id, item := range service.childParentLinks {
		if item.RecordedAt.Before(cutoff) {
			delete(service.childParentLinks, id)
		}
	}
}

func (service *Service) childParentLinkFor(requestID string) (childParentLink, bool) {
	if service == nil {
		return childParentLink{}, false
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return childParentLink{}, false
	}
	service.childParentLinkMu.Lock()
	defer service.childParentLinkMu.Unlock()
	link, ok := service.childParentLinks[requestID]
	if !ok || link.empty() {
		return childParentLink{}, false
	}
	return link, true
}

// conversationIDForRequest 把一个 request_id 解析成它所属的 conversation_id。
// 优先查 native 委派登记（父流可能已经换过 request，但登记里保留了原始归属），
// 再回退到 broker 里仍然活着的流。
func (service *Service) conversationIDForRequest(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if service == nil || requestID == "" {
		return ""
	}
	service.delegationRuntimeMu.Lock()
	for _, item := range service.nativeDelegations {
		if item == nil {
			continue
		}
		if strings.TrimSpace(item.ParentRequestID) == requestID {
			conversationID := strings.TrimSpace(item.ConversationID)
			if conversationID != "" {
				service.delegationRuntimeMu.Unlock()
				return conversationID
			}
		}
	}
	service.delegationRuntimeMu.Unlock()
	if stream, ok := service.broker.Get(requestID); ok && stream != nil {
		stream.mu.Lock()
		conversationID := strings.TrimSpace(stream.ConversationID)
		stream.mu.Unlock()
		return conversationID
	}
	return ""
}

// applyChildParentLink 在子会话元数据上补齐父链路。只填空字段，绝不覆盖已有值，
// 避免 rewind / 导入历史时把已经正确的归属改掉。返回是否发生了改动。
func (service *Service) applyChildParentLink(conversation *ConversationFile, requestID string) bool {
	if service == nil || conversation == nil {
		return false
	}
	link, ok := service.childParentLinkFor(requestID)
	if !ok {
		return false
	}
	return service.applyResolvedChildParentLink(conversation, link)
}

func (service *Service) applyResolvedChildParentLink(conversation *ConversationFile, link childParentLink) bool {
	if conversation == nil {
		return false
	}
	changed := false
	if strings.TrimSpace(conversation.ParentToolCallID) == "" && strings.TrimSpace(link.ParentToolCallID) != "" {
		conversation.ParentToolCallID = strings.TrimSpace(link.ParentToolCallID)
		changed = true
	}
	parentConversationID := ""
	if strings.TrimSpace(conversation.ParentConversationID) == "" {
		parentConversationID = service.conversationIDForRequest(link.ParentRequestID)
		if parentConversationID != "" && parentConversationID != strings.TrimSpace(conversation.ConversationID) {
			conversation.ParentConversationID = parentConversationID
			changed = true
		}
	}
	// 根会话默认等于自身（bootstrapRuntimeConversation 的兜底）。子代理必须改指向根 turn，
	// 否则 skill_store / context_projection 这些按根会话聚合的路径会把子代理当独立顶层会话。
	rootConversationID := service.conversationIDForRequest(link.RootParentRequestID)
	if rootConversationID == "" {
		rootConversationID = firstNonEmpty(strings.TrimSpace(conversation.ParentConversationID), parentConversationID)
	}
	if rootConversationID != "" && rootConversationID != strings.TrimSpace(conversation.ConversationID) {
		current := strings.TrimSpace(conversation.RootConversationID)
		if current == "" || current == strings.TrimSpace(conversation.ConversationID) {
			conversation.RootConversationID = rootConversationID
			changed = true
		}
	}
	return changed
}

// backfillChildParentLink 处理「run_request 先于 RunSSE 到达」的顺序：会话此时已经建好且
// 父字段为空，需要在头到达后补写一次磁盘元数据，并同步已加载的 checkpoint 快照。
func (service *Service) backfillChildParentLink(requestID string, link childParentLink) {
	if service == nil {
		return
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return
	}
	stream.mu.Lock()
	conversationID := strings.TrimSpace(stream.ConversationID)
	checkpoint := stream.CheckpointConversation
	stream.mu.Unlock()
	if checkpoint != nil {
		stream.mu.Lock()
		service.applyResolvedChildParentLink(checkpoint, link)
		stream.mu.Unlock()
	}
	if conversationID == "" || service.store == nil {
		return
	}
	if _, err := service.store.UpdateConversationMeta(conversationID, func(conversation *ConversationFile) error {
		service.applyResolvedChildParentLink(conversation, link)
		return nil
	}); err != nil {
		return
	}
}

// nativeParentBindingForChild 解析一个子流应该把可见输出镜像到哪个父 Task 气泡。
// 优先使用会话元数据（本次修复后新建的子会话都有），再回退到 RunSSE 头记录，
// 后者覆盖「头晚到」以及本次修复之前落盘、元数据缺父信息的历史会话。
func (service *Service) nativeParentBindingForChild(child *ActiveStream, conversation *ConversationFile) (parentRequestID string, parentToolCallID string) {
	if service == nil || child == nil {
		return "", ""
	}
	child.mu.Lock()
	childRequestID := strings.TrimSpace(child.RequestID)
	childConversationID := strings.TrimSpace(child.ConversationID)
	child.mu.Unlock()

	parentConversationID := ""
	if conversation != nil {
		parentConversationID = strings.TrimSpace(conversation.ParentConversationID)
		parentToolCallID = strings.TrimSpace(conversation.ParentToolCallID)
	}
	if parentConversationID != "" && parentToolCallID != "" && childConversationID != "" {
		if requestID := service.nativeDelegationParentRequestID(parentConversationID, parentToolCallID); requestID != "" {
			return requestID, parentToolCallID
		}
	}
	link, ok := service.childParentLinkFor(childRequestID)
	if !ok {
		return "", ""
	}
	parentToolCallID = firstNonEmpty(strings.TrimSpace(link.ParentToolCallID), parentToolCallID)
	if parentToolCallID == "" {
		return "", ""
	}
	if resolved := service.conversationIDForRequest(link.ParentRequestID); resolved != "" {
		if requestID := service.nativeDelegationParentRequestID(resolved, parentToolCallID); requestID != "" {
			return requestID, parentToolCallID
		}
	}
	return "", ""
}

// childConversationIDForNativeDelegation 反查某个 native 委派对应的子 conversation。
// 索引来源是子代理 RunSSE 携带的 (x-parent-request-id, x-parent-agent-tool-call-id)，
// 它们分别等于委派登记里的 ParentRequestID 与 ToolCallID。
func (service *Service) childConversationIDForNativeDelegation(item *nativeDelegationRuntime) string {
	if service == nil || item == nil {
		return ""
	}
	parentRequestID := strings.TrimSpace(item.ParentRequestID)
	parentToolCallID := strings.TrimSpace(item.ToolCallID)
	if parentRequestID == "" || parentToolCallID == "" {
		return ""
	}
	childRequestID := ""
	service.childParentLinkMu.Lock()
	for requestID, link := range service.childParentLinks {
		if strings.TrimSpace(link.ParentRequestID) == parentRequestID && strings.TrimSpace(link.ParentToolCallID) == parentToolCallID {
			childRequestID = requestID
			break
		}
	}
	service.childParentLinkMu.Unlock()
	if childRequestID == "" {
		return ""
	}
	stream, ok := service.broker.Get(childRequestID)
	if !ok || stream == nil {
		return ""
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return strings.TrimSpace(stream.ConversationID)
}

// nativeDelegationParentRequestID 找出仍在运行、且归属 (conversation, tool_call) 的
// native 委派所属父 request。
func (service *Service) nativeDelegationParentRequestID(parentConversationID string, parentToolCallID string) string {
	if service == nil {
		return ""
	}
	parentConversationID = strings.TrimSpace(parentConversationID)
	parentToolCallID = strings.TrimSpace(parentToolCallID)
	if parentConversationID == "" || parentToolCallID == "" {
		return ""
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	for _, item := range service.nativeDelegations {
		if item == nil || delegatedStatusTerminal(item.Status) {
			continue
		}
		if strings.TrimSpace(item.ConversationID) == parentConversationID && strings.TrimSpace(item.ToolCallID) == parentToolCallID {
			return strings.TrimSpace(item.ParentRequestID)
		}
	}
	return ""
}
