package forwarder

import (
	"encoding/json"
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func TestBuildTurnSummaryText(t *testing.T) {
	t.Run("user_and_assistant", func(t *testing.T) {
		got := buildTurnSummaryText("实现登录功能", "已完成登录，支持 token 刷新。", &ConversationFile{})
		want := "实现登录功能：已完成登录，支持 token 刷新。"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("user_only", func(t *testing.T) {
		got := buildTurnSummaryText("解释一下依赖注入", "", &ConversationFile{})
		if got != "解释一下依赖注入" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("assistant_only", func(t *testing.T) {
		got := buildTurnSummaryText("", "只有助手回复没有用户消息", &ConversationFile{})
		if got != "只有助手回复没有用户消息" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("fallback_to_conversation_entry", func(t *testing.T) {
		payload, err := json.Marshal(assistantTextPayload{Text: "来自历史条目的回复"})
		if err != nil {
			t.Fatal(err)
		}
		conversation := &ConversationFile{Entries: []HistoryEntry{
			{Kind: "assistant_text", Payload: payload},
		}}
		got := buildTurnSummaryText("修复 bug", "", conversation)
		if got != "修复 bug：来自历史条目的回复" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("truncation_keeps_rune_boundary", func(t *testing.T) {
		got := buildTurnSummaryText(strings.Repeat("很长的用户消息", 50), strings.Repeat("很长的助手回复", 60), &ConversationFile{})
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("expected ellipsis suffix, got %q", got)
		}
		runes := []rune(got)
		if runes[len(runes)-1] != '…' {
			t.Fatalf("expected ellipsis last rune")
		}
	})
	t.Run("exact_max_runes_no_ellipsis", func(t *testing.T) {
		text := strings.Repeat("字", 96)
		got := buildTurnSummaryText(text, "", &ConversationFile{})
		if got != text {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("empty_returns_empty", func(t *testing.T) {
		if got := buildTurnSummaryText("", "", &ConversationFile{}); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestEmitTurnSummary(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	reqID := "req-summary-1"
	convID := "conv-summary-1"
	stream, err := service.broker.OpenStream(reqID, convID, 7, "deepseek-v4", "DeepSeek V4", agentv1.AgentMode_AGENT_MODE_AGENT, "实现登录功能")
	if err != nil {
		t.Fatal(err)
	}
	if stream == nil {
		t.Fatal("stream is nil")
	}
	stream.CheckpointConversation = &ConversationFile{ConversationID: convID, Mode: "agent"}
	stream.ProviderAccumulatedText = "已完成登录功能。"

	if err := service.emitTurnSummary(stream, reqID, "call-1"); err != nil {
		t.Fatal(err)
	}
	if len(stream.Backlog) != 3 {
		t.Fatalf("expected 3 summary events, got %d", len(stream.Backlog))
	}
	types := make([]string, 0, 3)
	for _, event := range stream.Backlog {
		update := event.Message.GetInteractionUpdate()
		if update == nil {
			t.Fatalf("expected interaction update event")
		}
		switch update.Message.(type) {
		case *agentv1.InteractionUpdate_SummaryStarted:
			types = append(types, "started")
		case *agentv1.InteractionUpdate_Summary:
			summary := update.GetSummary()
			if summary == nil || !strings.Contains(summary.GetSummary(), "实现登录功能") {
				t.Fatalf("summary content mismatch: %+v", summary)
			}
			types = append(types, "summary")
		case *agentv1.InteractionUpdate_SummaryCompleted:
			completed := update.GetSummaryCompleted()
			if completed == nil || completed.GetHookMessage() != reqID {
				t.Fatalf("summary completed hook_message mismatch: %+v", completed)
			}
			types = append(types, "completed")
		default:
			t.Fatalf("unexpected event type: %T", update.Message)
		}
	}
	if strings.Join(types, ",") != "started,summary,completed" {
		t.Fatalf("event order mismatch: %v", types)
	}

	// 同一 turn 再次调用应去重，不追加事件。
	if err := service.emitTurnSummary(stream, reqID, "call-1"); err != nil {
		t.Fatal(err)
	}
	if len(stream.Backlog) != 3 {
		t.Fatalf("expected dedupe within same turn, got %d events", len(stream.Backlog))
	}

	// 新 turn 应重新发送。
	stream.TurnSeq = 8
	stream.SummaryEmittedTurn = 7
	stream.ProviderAccumulatedText = "第二轮完成。"
	if err := service.emitTurnSummary(stream, reqID, "call-2"); err != nil {
		t.Fatal(err)
	}
	if len(stream.Backlog) != 6 {
		t.Fatalf("expected 3 new events for next turn, got %d total", len(stream.Backlog))
	}
}

func TestEmitTurnSummarySkipsEmptyText(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	reqID := "req-summary-2"
	convID := "conv-summary-2"
	stream, err := service.broker.OpenStream(reqID, convID, 1, "deepseek-v4", "DeepSeek V4", agentv1.AgentMode_AGENT_MODE_AGENT, "")
	if err != nil {
		t.Fatal(err)
	}
	if stream == nil {
		t.Fatal("stream is nil")
	}
	stream.CheckpointConversation = &ConversationFile{ConversationID: convID, Mode: "agent"}
	if err := service.emitTurnSummary(stream, reqID, "call-1"); err != nil {
		t.Fatal(err)
	}
	if len(stream.Backlog) != 0 {
		t.Fatalf("expected no events for empty summary, got %d", len(stream.Backlog))
	}
	if stream.SummaryEmittedTurn != 0 {
		t.Fatalf("expected no emitted turn recorded, got %d", stream.SummaryEmittedTurn)
	}
}
