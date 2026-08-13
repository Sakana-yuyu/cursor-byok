"""把镜像抓包里的协议字节解成带完整字段名的文本（用 protoc --decode，不需要 protobuf 运行时）。

  服务端帧：protocolFrame.frameBase64 -> Connect 帧（5 字节头 + payload）-> agent.v1.AgentServerMessage
  客户端体：bodyBase64 -> aiserver.v1.BidiAppendRequest -> 内层 agent.v1.AgentClientMessage

用法：
  # 1. 先生成 descriptor set（protoc 需自行安装）
  protoc --include_imports --descriptor_set_out=<desc> -I proto proto/agent_v1.proto
  # 2. 解码
  python scripts/protocol-capture/decode.py <official.raw.jsonl> <out_dir> <desc> agent_v1.proto [skip_kinds]

参数：
  skip_kinds  逗号分隔的 kind 白噪声过滤，默认跳过纯增量帧（text_delta 等），只看结构性消息。

输出：
  <out_dir>/server-frames.txt    解码后的 AgentServerMessage
  <out_dir>/client-messages.txt  解码后的 AgentClientMessage

注意事项：
  - descriptor set 的来源必须是当前仓库的 proto，且要先确认目标字段号存在。历史教训：曾经
    proto/agent_v1.proto 的 ToolCall oneof 缺 send_to_user(58) / pi_*(61-67) / search_conversations(69) /
    create_goal(70) / update_goal(71)，用它解码会把这些 arm 静默丢进 unknown fields。该缺口已经修复，
    2026-08-13 复核：proto/agent_v1.proto、proto/from_extensions/agent_v1.proto 与 gen/agentv1 三者
    都带上述字段号，两份 proto 仅差 go_package 和 3 处已解析的消息类型（proto/agent_v1.proto 更完整，
    也是 gen/ 的来源，优先用它）。换 proto 前请照此重新核对字段号，别凭旧结论。
  - gzip 兜底：记录器现在会自己解压 Content-Encoding: gzip 的请求体，摘要字段不再是空的；这里的
    gunzip 分支保留给修复之前采到的旧抓包（那些记录带 decodeError=unsupported_content_encoding，
    但 bodyBase64 是完整原始字节）。
"""
import base64
import binascii
import gzip
import json
import os
import subprocess
import sys

if len(sys.argv) < 5:
    raise SystemExit(__doc__)

RAW = sys.argv[1]
OUT = sys.argv[2]
DESC = sys.argv[3]
PROTO = sys.argv[4]
SKIP_KINDS = set((sys.argv[5] if len(sys.argv) > 5 else
                  "text_delta,token_delta,thinking_delta,heartbeat,set_blob_args").split(","))

os.makedirs(OUT, exist_ok=True)


def protoc_decode(payload, message_type):
    proc = subprocess.run(
        ["protoc", "--decode=" + message_type, "--descriptor_set_in=" + DESC, PROTO],
        input=payload, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    text = proc.stdout.decode("utf-8", "replace")
    if proc.returncode != 0:
        text += "\n#DECODE_ERROR " + proc.stderr.decode("utf-8", "replace")[:400]
    return text


def read_varint(buf, pos):
    result = 0
    shift = 0
    while True:
        byte = buf[pos]
        pos += 1
        result |= (byte & 0x7F) << shift
        if not byte & 0x80:
            return result, pos
        shift += 7


def parse_bidi_append(buf):
    """不依赖 protobuf 运行时，从 BidiAppendRequest 里取出 (data_hex, data_binary)。"""
    data_hex = None
    data_binary = None
    pos = 0
    while pos < len(buf):
        try:
            key, pos = read_varint(buf, pos)
        except IndexError:
            break
        field, wire = key >> 3, key & 7
        if wire == 0:
            _, pos = read_varint(buf, pos)
        elif wire == 2:
            length, pos = read_varint(buf, pos)
            chunk = buf[pos:pos + length]
            pos += length
            if field == 1:
                data_hex = chunk.decode("utf-8", "replace")
            elif field == 4:
                data_binary = chunk
        elif wire == 5:
            pos += 4
        elif wire == 1:
            pos += 8
        else:
            break
    return data_hex, data_binary


server_out = open(os.path.join(OUT, "server-frames.txt"), "w", encoding="utf-8")
client_out = open(os.path.join(OUT, "client-messages.txt"), "w", encoding="utf-8")
stats = {"server_decoded": 0, "server_skipped": 0, "client_decoded": 0, "client_skipped": 0}

line_no = 0
with open(RAW, "r", encoding="utf-8", errors="replace") as handle:
    for line in handle:
        line_no += 1
        line = line.strip()
        if not line:
            continue
        try:
            rec = json.loads(line)
        except Exception:
            continue

        frame = rec.get("protocolFrame") or {}
        if frame.get("frameBase64"):
            kind = frame.get("serverDetailKind") or frame.get("serverMessageKind") or ""
            exec_kind = frame.get("execMessageKind") or ""
            if kind in SKIP_KINDS and not exec_kind:
                stats["server_skipped"] += 1
                continue
            blob = base64.b64decode(frame["frameBase64"])
            if len(blob) < 5:
                continue
            flags = blob[0]
            payload = blob[5:]
            if flags & 0x01:
                try:
                    payload = gzip.decompress(payload)
                except Exception:
                    continue
            if flags & 0x02:
                continue
            text = protoc_decode(payload, "agent.v1.AgentServerMessage")
            server_out.write(
                "===== line=%d ts=%s exchange=%s seq=%s kind=%s detail=%s exec=%s bytes=%s\n"
                % (line_no, rec.get("ts"), rec.get("exchangeId"), frame.get("sequence"),
                   frame.get("serverMessageKind"), frame.get("serverDetailKind"),
                   exec_kind, frame.get("frameBytes")))
            server_out.write(text + "\n")
            stats["server_decoded"] += 1
            continue

        if rec.get("phase") == "request" and rec.get("bodyBase64") and \
                "BidiAppend" in (rec.get("url") or ""):
            proto_summary = rec.get("protocol") or {}
            kind = proto_summary.get("clientDetailKind") or proto_summary.get("clientMessageKind") or ""
            if kind and kind in SKIP_KINDS:
                stats["client_skipped"] += 1
                continue
            body = base64.b64decode(rec["bodyBase64"])
            headers = {k.lower(): v for k, v in (rec.get("headers") or {}).items()}
            if "gzip" in headers.get("content-encoding", "").lower():
                try:
                    body = gzip.decompress(body)
                except Exception:
                    continue
            data_hex, data_binary = parse_bidi_append(body)
            payload = data_binary
            if not payload and data_hex:
                try:
                    payload = binascii.unhexlify(data_hex.strip())
                except Exception:
                    payload = None
            if not payload:
                continue
            text = protoc_decode(payload, "agent.v1.AgentClientMessage")
            client_out.write(
                "===== line=%d ts=%s exchange=%s kind=%s detail=%s result=%s mode=%s\n"
                % (line_no, rec.get("ts"), rec.get("exchangeId"),
                   proto_summary.get("clientMessageKind"), proto_summary.get("clientDetailKind"),
                   proto_summary.get("clientResultKind"), proto_summary.get("agentMode")))
            client_out.write(text + "\n")
            stats["client_decoded"] += 1

server_out.close()
client_out.close()
print(json.dumps(stats))
