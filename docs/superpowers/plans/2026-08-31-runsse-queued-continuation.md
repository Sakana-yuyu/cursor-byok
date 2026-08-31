# RunSSE 排队续跑修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Cursor 在成功 `turnEnded` 后能够通过同一 request 提交排队消息的 `conversation_action`，避免 RunSSE 过早关闭造成旧 generation 卡死。

**Architecture:** 保留 broker 已产生的成功 End，但在 RunSSE 返回前增加 500ms 的有界 continuation 宽限。宽限期间通过 cursor 后的新 backlog 或流从终态恢复来识别 `reopenTerminalStreamForNewTurn`，命中后继续原 RunSSE；失败和取消终态仍立即返回。

**Tech Stack:** Go 1.25、Connect RPC server stream、现有 `StreamBroker`、Go `testing`。

## Global Constraints

- 不修改安装版 Cursor、bundle、签名或 feature gate。
- 不修改或提交工作区已有的无关文件。
- 只延迟无错误的成功 End；失败、取消和 provider error 保持立即返回。
- 先看到聚焦回归测试按预期失败，再写生产实现。
- 不新增依赖，不引入配置项或新抽象层。

---

### Task 1: 锁定成功终态 continuation 判定

**Files:**
- Create: `internal/backend/forwarder/service_runsse_continuation_test.go`
- Modify: `internal/backend/forwarder/service.go`

**Interfaces:**
- Consumes: `StreamBroker.ReadFromCursor`、`Service.reopenTerminalStreamForNewTurn`、`ActiveStream.Status/Phase`。
- Produces: `waitForRunSSEContinuation(ctx context.Context, requestID string, cursor int, signal <-chan struct{}, grace time.Duration) bool`。

- [ ] **Step 1: 写同 request 重开失败测试**

测试创建已成功完成的 stream，记录旧 End 后的 cursor；在 helper 等待期间调用 `reopenTerminalStreamForNewTurn` 并发布新回合消息，断言 helper 返回 `true`。

```go
func TestWaitForRunSSEContinuationDetectsReopenedRequest(t *testing.T) {
    service, stream, signal, cursor := completedRunSSETestStream(t)
    go func() {
        time.Sleep(10 * time.Millisecond)
        if err := service.reopenTerminalStreamForNewTurn(stream); err != nil {
            return
        }
        _ = service.broker.Publish(stream.RequestID, StreamEvent{Message: buildHeartbeatMessage()})
    }()

    if !service.waitForRunSSEContinuation(context.Background(), stream.RequestID, cursor, signal, 200*time.Millisecond) {
        t.Fatal("reopened request was not detected")
    }
}
```

- [ ] **Step 2: 运行测试确认 RED**

Run: `go test ./internal/backend/forwarder -run TestWaitForRunSSEContinuationDetectsReopenedRequest -count=1`

Expected: FAIL，原因是 `waitForRunSSEContinuation` 尚不存在。

- [ ] **Step 3: 写最小 continuation helper**

helper 在截止时间前循环执行：

```go
if backlog, err := service.broker.ReadFromCursor(requestID, cursor); err == nil && len(backlog) > 0 {
    return true
}
if stream status/phase is non-terminal {
    return true
}
select {
case <-ctx.Done():
    return false
case <-signal:
    continue
case <-timer.C:
    return finalStateCheck()
}
```

- [ ] **Step 4: 补充无 continuation 和旧 signal 边界测试**

添加两个测试：宽限到期返回 `false`；只有旧 signal、没有新 backlog 或重开时仍返回 `false`。

- [ ] **Step 5: 运行聚焦测试确认 GREEN**

Run: `go test ./internal/backend/forwarder -run 'TestWaitForRunSSEContinuation' -count=1`

Expected: PASS。

### Task 2: 接入 RunSSE 成功终态并验证真实行为

**Files:**
- Modify: `internal/backend/forwarder/service.go`
- Modify: `internal/backend/forwarder/service_runsse_continuation_test.go`

**Interfaces:**
- Consumes: Task 1 的 `waitForRunSSEContinuation`。
- Produces: RunSSE 成功 End 在 500ms 内允许同 request continuation；其他终态保持原语义。

- [ ] **Step 1: 添加终态分类测试**

测试用成功、失败和取消 `StreamEvent` 验证：只有成功 End 才允许进入 continuation 等待。

- [ ] **Step 2: 运行分类测试确认 RED**

Run: `go test ./internal/backend/forwarder -run 'TestRunSSETerminalContinuation' -count=1`

Expected: FAIL，原因是 RunSSE 尚未使用 continuation 判定。

- [ ] **Step 3: 在 RunSSE End 分支接入最小修复**

```go
if event.End {
    if event.TerminalErrorCode == "" && event.TerminalErrorMessage == "" &&
        service.waitForRunSSEContinuation(ctx, requestID, cursor, signal, runSSEContinuationGracePeriod) {
        continue
    }
    return buildTerminalStreamError(event)
}
```

生产宽限固定为 `500 * time.Millisecond`，不增加用户配置。

- [ ] **Step 4: 运行 forwarder 聚焦测试**

Run: `go test ./internal/backend/forwarder -run 'TestWaitForRunSSEContinuation|TestRunSSETerminalContinuation' -count=1`

Expected: PASS。

- [ ] **Step 5: 运行包级回归与静态检查**

Run: `go test ./internal/backend/forwarder -count=1`

Run: `go test ./internal/backend/... -count=1`

Run: `gofmt -w internal/backend/forwarder/service.go internal/backend/forwarder/service_runsse_continuation_test.go`

Run: `git diff --check`

Expected: 全部退出码为 0。

- [ ] **Step 6: 独立提交实现**

```bash
git add internal/backend/forwarder/service.go internal/backend/forwarder/service_runsse_continuation_test.go
git commit -m "fix: 保留 RunSSE 排队续跑窗口"
```

- [ ] **Step 7: 真实 Cursor 队列验收**

构建并运行当前仓库二进制，使用同一 Cursor 任务连续发送一条正在运行的消息和至少两条排队消息，核对：

- 第一条完成后第二条自动产生 `conversation_action` 或继续的 provider pass。
- 原 RunSSE 在旧成功 End 后没有立即关闭并触发 `WriteIterableClosedError`。
- 不再出现旧 request UUID 对应的 `Simulated thinking timeout without active turn tracker`。
- 第三条同样自动续跑，证明不是一次性偶然成功。
- 手动重发不计入验收结果。
