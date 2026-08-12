package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cursor/internal/appdata"
)

const cursorProtocolTimelineFileName = "protocol.timeline.jsonl"

// CursorProtocolSession 是按匿名请求哈希聚合的 Cursor 协议结构摘要。
// 字段严格对应安全时间线白名单，禁止承载原始帧、正文或任何凭据。
type CursorProtocolSession struct {
	RequestIDHash   string                `json:"requestIdHash"`
	FirstSeenAtUnix int64                 `json:"firstSeenAtUnixMs"`
	LastSeenAtUnix  int64                 `json:"lastSeenAtUnixMs"`
	EventCount      int                   `json:"eventCount"`
	UpstreamCount   int                   `json:"upstreamCount"`
	DownstreamCount int                   `json:"downstreamCount"`
	AgentMode       string                `json:"agentMode,omitempty"`
	Multitask       bool                  `json:"multitask,omitempty"`
	SubagentActions []string              `json:"subagentActions,omitempty"`
	Terminal        bool                  `json:"terminal,omitempty"`
	DecodeErrors    []string              `json:"decodeErrors,omitempty"`
	Events          []CursorProtocolEvent `json:"events"`
}

// CursorProtocolEvent 是一条安全协议时间线事件的对外白名单表示。
type CursorProtocolEvent struct {
	TimestampUnix     int64  `json:"timestampUnixMs"`
	Direction         string `json:"direction"`
	Sequence          int    `json:"sequence"`
	EventKind         string `json:"eventKind"`
	ClientMessageKind string `json:"clientMessageKind,omitempty"`
	ClientDetailKind  string `json:"clientDetailKind,omitempty"`
	ClientResultKind  string `json:"clientResultKind,omitempty"`
	AgentMode         string `json:"agentMode,omitempty"`
	Multitask         bool   `json:"multitask,omitempty"`
	SubagentAction    string `json:"subagentAction,omitempty"`
	ServerMessageKind string `json:"serverMessageKind,omitempty"`
	ServerDetailKind  string `json:"serverDetailKind,omitempty"`
	ExecMessageKind   string `json:"execMessageKind,omitempty"`
	StreamContentKind string `json:"streamContentKind,omitempty"`
	StreamDeltaBytes  *int   `json:"streamDeltaBytes,omitempty"`
	Terminal          bool   `json:"terminal,omitempty"`
	DecodeError       string `json:"decodeError,omitempty"`
}

// cursorProtocolTimelineLine 只声明时间线允许读取的字段。未知字段被 json 解码器忽略，
// 因此即使文件中混入其他调试字段，也不能越过 DTO 白名单。
type cursorProtocolTimelineLine struct {
	TS                time.Time `json:"ts"`
	RequestIDHash     string    `json:"requestIdHash"`
	Direction         string    `json:"direction"`
	Sequence          int       `json:"sequence"`
	EventKind         string    `json:"eventKind"`
	ClientMessageKind string    `json:"clientMessageKind"`
	ClientDetailKind  string    `json:"clientDetailKind"`
	ClientResultKind  string    `json:"clientResultKind"`
	AgentMode         string    `json:"agentMode"`
	Multitask         bool      `json:"multitask"`
	SubagentAction    string    `json:"subagentAction"`
	ServerMessageKind string    `json:"serverMessageKind"`
	ServerDetailKind  string    `json:"serverDetailKind"`
	ExecMessageKind   string    `json:"execMessageKind"`
	StreamContentKind string    `json:"streamContentKind"`
	StreamDeltaBytes  *int      `json:"streamDeltaBytes"`
	Terminal          bool      `json:"terminal"`
	DecodeError       string    `json:"decodeError"`
}

func scanCursorProtocolSessions() ([]CursorProtocolSession, error) {
	return scanCursorProtocolSessionsIn(appdata.HistoryRootPath())
}

// scanCursorProtocolSessionsIn 按 requestIdHash 聚合固定安全时间线文件。
// 不存在代表当前目录未执行隔离采集；其他读取错误必须由调用者感知。
func scanCursorProtocolSessionsIn(historyRoot string) ([]CursorProtocolSession, error) {
	path := filepath.Join(historyRoot, "_debug", "mirror", cursorProtocolTimelineFileName)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []CursorProtocolSession{}, nil
		}
		return nil, fmt.Errorf("open cursor protocol timeline: %w", err)
	}
	defer func() { _ = file.Close() }()

	byRequest := make(map[string]*CursorProtocolSession)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var line cursorProtocolTimelineLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		requestIDHash := strings.TrimSpace(line.RequestIDHash)
		if requestIDHash == "" {
			continue
		}
		session := byRequest[requestIDHash]
		if session == nil {
			session = &CursorProtocolSession{RequestIDHash: requestIDHash, Events: make([]CursorProtocolEvent, 0, 8)}
			byRequest[requestIDHash] = session
		}
		appendCursorProtocolTimelineLine(session, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read cursor protocol timeline: %w", err)
	}

	sessions := make([]CursorProtocolSession, 0, len(byRequest))
	for _, session := range byRequest {
		sort.SliceStable(session.Events, func(left, right int) bool {
			if session.Events[left].TimestampUnix != session.Events[right].TimestampUnix {
				return session.Events[left].TimestampUnix < session.Events[right].TimestampUnix
			}
			return session.Events[left].Sequence < session.Events[right].Sequence
		})
		sort.Strings(session.SubagentActions)
		sort.Strings(session.DecodeErrors)
		sessions = append(sessions, *session)
	}
	sort.SliceStable(sessions, func(left, right int) bool {
		if sessions[left].LastSeenAtUnix != sessions[right].LastSeenAtUnix {
			return sessions[left].LastSeenAtUnix > sessions[right].LastSeenAtUnix
		}
		return sessions[left].RequestIDHash < sessions[right].RequestIDHash
	})
	return sessions, nil
}

func appendCursorProtocolTimelineLine(session *CursorProtocolSession, line cursorProtocolTimelineLine) {
	if session == nil {
		return
	}
	timestamp := line.TS.UnixMilli()
	if session.FirstSeenAtUnix == 0 || (timestamp != 0 && timestamp < session.FirstSeenAtUnix) {
		session.FirstSeenAtUnix = timestamp
	}
	if timestamp > session.LastSeenAtUnix {
		session.LastSeenAtUnix = timestamp
	}
	session.EventCount++
	if line.Direction == "request" {
		session.UpstreamCount++
	} else if line.Direction == "response" {
		session.DownstreamCount++
	}
	if session.AgentMode == "" {
		session.AgentMode = strings.TrimSpace(line.AgentMode)
	}
	session.Multitask = session.Multitask || line.Multitask
	session.Terminal = session.Terminal || line.Terminal
	session.SubagentActions = appendUniqueCursorProtocolValue(session.SubagentActions, line.SubagentAction)
	session.DecodeErrors = appendUniqueCursorProtocolValue(session.DecodeErrors, line.DecodeError)
	session.Events = append(session.Events, CursorProtocolEvent{
		TimestampUnix:     timestamp,
		Direction:         strings.TrimSpace(line.Direction),
		Sequence:          line.Sequence,
		EventKind:         strings.TrimSpace(line.EventKind),
		ClientMessageKind: strings.TrimSpace(line.ClientMessageKind),
		ClientDetailKind:  strings.TrimSpace(line.ClientDetailKind),
		ClientResultKind:  strings.TrimSpace(line.ClientResultKind),
		AgentMode:         strings.TrimSpace(line.AgentMode),
		Multitask:         line.Multitask,
		SubagentAction:    strings.TrimSpace(line.SubagentAction),
		ServerMessageKind: strings.TrimSpace(line.ServerMessageKind),
		ServerDetailKind:  strings.TrimSpace(line.ServerDetailKind),
		ExecMessageKind:   strings.TrimSpace(line.ExecMessageKind),
		StreamContentKind: strings.TrimSpace(line.StreamContentKind),
		StreamDeltaBytes:  line.StreamDeltaBytes,
		Terminal:          line.Terminal,
		DecodeError:       strings.TrimSpace(line.DecodeError),
	})
}

func appendUniqueCursorProtocolValue(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
