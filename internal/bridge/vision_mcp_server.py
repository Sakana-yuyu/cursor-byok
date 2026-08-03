#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
vision_mcp_server.py — 读图 MCP server（自包含，仅依赖 Python 标准库）。

由 cursor-byok 内置并在「一键启用读图 MCP」时落盘到
~/.cursor/skills/image-see/scripts/vision_mcp_server.py。

行为：
  1. 通过 MCP stdio（newline-delimited JSON-RPC 2.0）暴露 read_image 工具。
  2. read_image 读取本地图片文件（或 http/https URL），转 base64 data URL，
     调用与视觉委派同一个网关（IMAGE_SEE_BASE_URL + IMAGE_SEE_ENDPOINT）识图，
     返回模型文本（画面描述 + OCR）。

环境变量：
  IMAGE_SEE_BASE_URL  视觉网关地址。可为 API 根（如 http://127.0.0.1:15721/v1）。
  IMAGE_SEE_ENDPOINT  请求端点（自动与视觉委派所用协议保持一致）：
                      /v1/chat/completions（默认）或 /v1/responses（Responses API）。
  IMAGE_SEE_API_KEY   可选，网关鉴权 Bearer Token；为空则不携带 Authorization。
  IMAGE_SEE_MODEL     识图模型名；缺省 gpt-5.6-luna。
"""

import base64
import json
import mimetypes
import os
import sys
import urllib.error
import urllib.request

SERVER_NAME = "vision-reader"
SERVER_VERSION = "1.0.0"

PROTOCOL_VERSION = "2024-11-05"
DEFAULT_MODEL = "gpt-5.6-luna"
DEFAULT_ENDPOINT = "/v1/chat/completions"
MAX_IMAGE_BYTES = 20 * 1024 * 1024  # 20MB
HTTP_TIMEOUT = 60

VISION_PROMPT = (
    "请按以下两点输出这张图片的内容：\n"
    "1. 画面描述：主体内容、布局、颜色、UI 结构或场景，以及画面传达的关键信息。\n"
    "2. 文字抄录（OCR）：完整抄录图中所有可见文字（含中文、英文、数字、符号）；"
    "表格用 Markdown 表格输出。若无文字则跳过此项。\n"
    "不要编造图中不存在的内容。"
)


def read_message():
    """从 stdin 读取一行 JSON-RPC 消息；EOF 返回 None。"""
    line = sys.stdin.buffer.readline()
    if not line:
        return None
    text = line.decode("utf-8", "replace").strip()
    if not text:
        return None
    try:
        return json.loads(text)
    except ValueError:
        return None


def send_message(message):
    """向 stdout 写一行 JSON-RPC 消息并立即刷出。"""
    payload = json.dumps(message, ensure_ascii=False).encode("utf-8")
    sys.stdout.buffer.write(payload + b"\n")
    sys.stdout.buffer.flush()


def send_result(request_id, result):
    send_message({"jsonrpc": "2.0", "id": request_id, "result": result})


def send_error(request_id, code, message):
    send_message({
        "jsonrpc": "2.0",
        "id": request_id,
        "error": {"code": code, "message": message},
    })


def base_url():
    return (os.environ.get("IMAGE_SEE_BASE_URL") or "").strip()


def api_key():
    return (os.environ.get("IMAGE_SEE_API_KEY") or "").strip()


def model_name():
    return (os.environ.get("IMAGE_SEE_MODEL") or "").strip() or DEFAULT_MODEL


def load_image_data_url(raw_path):
    """把本地文件或 http(s) URL 转成 data URL。"""
    raw_path = (raw_path or "").strip()
    if not raw_path:
        raise ValueError("image_path 不能为空")
    lower = raw_path.lower()
    if lower.startswith("http://") or lower.startswith("https://"):
        request = urllib.request.Request(raw_path, headers={"User-Agent": "vision-mcp/1.0"})
        with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT) as response:
            data = response.read(MAX_IMAGE_BYTES + 1)
        if len(data) > MAX_IMAGE_BYTES:
            raise ValueError("图片超过 20MB 上限")
        media_type = response.headers.get("Content-Type") or "image/jpeg"
    else:
        if not os.path.isfile(raw_path):
            raise ValueError("图片文件不存在: %s" % raw_path)
        size = os.path.getsize(raw_path)
        if size > MAX_IMAGE_BYTES:
            raise ValueError("图片超过 20MB 上限")
        with open(raw_path, "rb") as handle:
            data = handle.read()
        media_type = mimetypes.guess_type(raw_path)[0] or "image/jpeg"
    encoded = base64.b64encode(data).decode("ascii")
    return "data:%s;base64,%s" % (media_type, encoded)


def request_endpoint(base):
    """归一化为完整请求端点：base 已含 endpoint 时直接用，否则按路径段去重拼接。"""
    endpoint = (os.environ.get("IMAGE_SEE_ENDPOINT") or "").strip() or DEFAULT_ENDPOINT
    if not endpoint.startswith("/"):
        endpoint = "/" + endpoint
    base = base.rstrip("/")
    if base.lower().endswith(endpoint.lower()):
        return base
    base_segments = [segment for segment in base.split("/") if segment]
    endpoint_segments = [segment for segment in endpoint.split("/") if segment]
    # 去重 base 与 endpoint 重叠的路径段（如 base=.../v1 与 endpoint=/v1/chat/completions）。
    overlap = 0
    limit = min(len(base_segments), len(endpoint_segments))
    for count in range(1, limit + 1):
        if base_segments[-count:] == endpoint_segments[:count]:
            overlap = count
    if overlap > 0:
        return base + "/" + "/".join(endpoint_segments[overlap:])
    return base + endpoint


def uses_responses_api(base):
    endpoint = (os.environ.get("IMAGE_SEE_ENDPOINT") or "").strip() or DEFAULT_ENDPOINT
    return "responses" in endpoint.lower()


def extract_responses_text(result):
    """从 Responses API 响应中提取模型文本（output 里的 message content / output_text）。"""
    texts = []
    for item in result.get("output") or []:
        if not isinstance(item, dict):
            continue
        if item.get("type") == "message":
            for content in item.get("content") or []:
                if isinstance(content, dict):
                    if content.get("type") == "output_text":
                        value = content.get("text") or ""
                    else:
                        value = content.get("text") or ""
                    if value:
                        texts.append(value)
        elif item.get("type") == "output_text":
            value = item.get("text") or ""
            if value:
                texts.append(value)
    return "\n".join(texts).strip()


def extract_chat_completions_text(result):
    """从 chat/completions 响应中提取模型文本。"""
    choices = result.get("choices") or []
    if not choices:
        raise ValueError("视觉网关响应中没有 choices")
    content = choices[0].get("message", {}).get("content") or ""
    if isinstance(content, list):
        parts = []
        for part in content:
            if isinstance(part, dict):
                text = part.get("text") or ""
                if text:
                    parts.append(text)
        content = "\n".join(parts)
    return str(content).strip()


def describe_image(image_data_url, question):
    """调用与视觉委派同一协议的视觉网关识图，返回模型文本。"""
    base = base_url()
    if not base:
        raise ValueError("未配置 IMAGE_SEE_BASE_URL 环境变量，无法识图")
    prompt = VISION_PROMPT
    if question and question.strip():
        prompt += "\n\n针对这张图片，请额外回答以下问题：\n" + question.strip()
    payload = {
        "model": model_name(),
        "messages": [{
            "role": "user",
            "content": [
                {"type": "text", "text": prompt},
                {"type": "image_url", "image_url": {"url": image_data_url}},
            ],
        }],
    }
    body = json.dumps(payload).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    key = api_key()
    if key:
        headers["Authorization"] = "Bearer " + key
    request = urllib.request.Request(
        request_endpoint(base), data=body, headers=headers, method="POST"
    )
    try:
        with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT) as response:
            raw = response.read()
    except urllib.error.HTTPError as error:
        detail = error.read(512).decode("utf-8", "replace").strip()
        raise ValueError("视觉网关返回 HTTP %s: %s" % (error.code, detail))
    except urllib.error.URLError as error:
        raise ValueError("无法连接视觉网关 %s: %s" % (base, error.reason))
    try:
        result = json.loads(raw.decode("utf-8", "replace"))
    except ValueError:
        raise ValueError("视觉网关返回了非 JSON 响应")
    if uses_responses_api(base):
        text = extract_responses_text(result)
    else:
        text = extract_chat_completions_text(result)
    if not text:
        raise ValueError("识图模型返回了空结果")
    return text


def handle_call(params):
    if not isinstance(params, dict):
        return {"content": [{"type": "text", "text": "参数必须是对象"}], "isError": True}
    arguments = params.get("arguments") or {}
    image_path = arguments.get("image_path") or arguments.get("path") or ""
    question = arguments.get("question") or ""
    try:
        data_url = load_image_data_url(image_path)
        description = describe_image(data_url, question)
    except Exception as error:  # noqa: BLE001 —— 工具级错误需要回传给模型而不是崩溃
        return {"content": [{"type": "text", "text": str(error)}], "isError": True}
    return {"content": [{"type": "text", "text": description}]}


def handle_message(message):
    if not isinstance(message, dict):
        return
    request_id = message.get("id")
    method = message.get("method") or ""

    if method == "initialize":
        send_result(request_id, {
            "protocolVersion": PROTOCOL_VERSION,
            "capabilities": {"tools": {}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
        })
        return
    if method == "notifications/initialized":
        return
    if method == "ping":
        if request_id is not None:
            send_result(request_id, {})
        return
    if method == "tools/list":
        send_result(request_id, {
            "tools": [{
                "name": "read_image",
                "description": (
                    "读取一张本地图片文件（绝对路径）或图片 URL，调用视觉网关返回"
                    "画面描述与文字（OCR）。图片已由用户提供时请直接读取，不要在工作区搜索。"
                ),
                "inputSchema": {
                    "type": "object",
                    "properties": {
                        "image_path": {
                            "type": "string",
                            "description": "图片的本地绝对路径或 http(s) URL",
                        },
                        "question": {
                            "type": "string",
                            "description": "针对图片的额外问题，可选",
                        },
                    },
                    "required": ["image_path"],
                },
            }],
        })
        return
    if method == "tools/call":
        name = (params_name(message.get("params")) or "")
        if name in ("read_image", "see_image", "vision"):
            send_result(request_id, handle_call(message.get("params")))
        else:
            send_result(request_id, {
                "content": [{"type": "text", "text": "未知工具: %s" % name}],
                "isError": True,
            })
        return
    if request_id is not None:
        send_error(request_id, -32601, "method not found: %s" % method)


def params_name(params):
    if isinstance(params, dict):
        name = params.get("name")
        if isinstance(name, str):
            return name
    return ""


def main():
    while True:
        message = read_message()
        if message is None:
            break
        handle_message(message)


if __name__ == "__main__":
    try:
        main()
    except BrokenPipeError:
        sys.exit(0)