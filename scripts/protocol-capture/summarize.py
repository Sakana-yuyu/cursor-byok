"""镜像抓包的第一遍廉价汇总（不需要 protobuf 运行时）。

读取保真记录器写出的 official.raw.jsonl，只聚合记录里已有的结构字段，产出：
  <out_dir>/summary.json      各结构字段的直方图 + 请求 URL 计数
  <out_dir>/frames-index.jsonl 每个 RunSSE 协议帧的定位索引（行号 / 交换 / 序号 / 字节数）

用法：
  python scripts/protocol-capture/summarize.py <official.raw.jsonl> <out_dir>
"""
import collections
import json
import os
import sys

if len(sys.argv) < 3:
    raise SystemExit(__doc__)

raw_path = sys.argv[1]
out_dir = sys.argv[2]
os.makedirs(out_dir, exist_ok=True)

counters = collections.defaultdict(collections.Counter)
urls = collections.Counter()
exchanges = {}
frames_index = []

line_no = 0
with open(raw_path, "r", encoding="utf-8", errors="replace") as handle:
    for line in handle:
        line_no += 1
        line = line.strip()
        if not line:
            continue
        try:
            rec = json.loads(line)
        except Exception:
            counters["parse"]["json_error"] += 1
            continue
        phase = rec.get("phase", "")
        counters["phase"][phase] += 1
        url = rec.get("url") or ""
        if phase == "request" and url:
            urls[url.split("?")[0]] += 1
        headers = {k.lower(): v for k, v in (rec.get("headers") or {}).items()}
        if headers.get("content-encoding"):
            counters["contentEncoding"][headers["content-encoding"]] += 1
        proto_summary = rec.get("protocol") or {}
        for key in ("clientMessageKind", "clientDetailKind", "clientResultKind",
                    "agentMode", "decodeError", "clientPayloadSource"):
            if proto_summary.get(key):
                counters[key][proto_summary[key]] += 1
        frame = rec.get("protocolFrame") or {}
        if frame:
            for key in ("serverMessageKind", "serverDetailKind", "execMessageKind",
                        "streamContentKind", "decodeError", "connectCompression"):
                if frame.get(key):
                    counters[key][frame[key]] += 1
            frames_index.append({
                "line": line_no,
                "ts": rec.get("ts"),
                "exchangeId": rec.get("exchangeId"),
                "seq": frame.get("sequence"),
                "serverMessageKind": frame.get("serverMessageKind", ""),
                "serverDetailKind": frame.get("serverDetailKind", ""),
                "execMessageKind": frame.get("execMessageKind", ""),
                "bytes": frame.get("frameBytes"),
            })
        if rec.get("exchangeId"):
            exchanges.setdefault(rec["exchangeId"], {"first": rec.get("ts"), "url": url})

with open(os.path.join(out_dir, "frames-index.jsonl"), "w", encoding="utf-8") as handle:
    for item in frames_index:
        handle.write(json.dumps(item, ensure_ascii=False) + "\n")

report = {"lines": line_no, "exchanges": len(exchanges), "urls": urls.most_common(40)}
for key, counter in counters.items():
    report[key] = counter.most_common(80)

with open(os.path.join(out_dir, "summary.json"), "w", encoding="utf-8") as handle:
    json.dump(report, handle, ensure_ascii=False, indent=2)

print(json.dumps(report, ensure_ascii=False, indent=2)[:8000])
