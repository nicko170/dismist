#!/usr/bin/env python3
"""Print aggregate protobuf field shapes from private AP telemetry captures.

No protobuf or JSON values, payload bytes, addresses, or client identifiers are
printed. Inputs may be raw telemetry bodies or encoded AP protocol frames.
"""

import argparse
from collections import Counter
import gzip
import json
from pathlib import Path


def uvarint(data, offset):
    value = 0
    for shift in range(0, 70, 7):
        if offset >= len(data):
            raise ValueError("truncated varint")
        byte = data[offset]
        offset += 1
        value |= (byte & 127) << shift
        if byte < 128:
            return value, offset
    raise ValueError("long varint")


def fields(data):
    offset = 0
    while offset < len(data):
        tag, offset = uvarint(data, offset)
        number, wire = tag >> 3, tag & 7
        if not number:
            raise ValueError("zero field")
        if wire == 0:
            _, offset = uvarint(data, offset)
            value = None
        elif wire in (1, 5):
            size = 8 if wire == 1 else 4
            value = data[offset : offset + size]
            offset += size
        elif wire == 2:
            size, offset = uvarint(data, offset)
            value = data[offset : offset + size]
            offset += size
        else:
            raise ValueError("unsupported wire type")
        if offset > len(data):
            raise ValueError("truncated field")
        yield number, wire, value


def telemetry_body(data):
    if data[:1] in (b"P", b"{") or data[:3] == b"\x02\x1f\x8b":
        return data
    if len(data) < 4:
        return None
    try:
        size, offset = uvarint(data, 2)
        subject = data[offset : offset + size]
        offset += size
        _, offset = uvarint(data, offset)
        if subject == b"ep-telemetry":
            return data[offset:]
    except ValueError:
        pass
    return None


def unpack(data):
    if data[:3] == b"\x02\x1f\x8b":
        data = gzip.decompress(data[1:])
        if len(data) > 4 << 20:
            raise ValueError("expanded body too large")
    if data[:1] == b"{":
        item = json.loads(data)
        topic = item.get("Topic")
        if topic in {"ep-config-applied-", "ep-pace-event-"}:
            return topic, None
        topics = item.get("Topics")
        if isinstance(topics, list) and all(isinstance(value, str) for value in topics) and set(topics) == {"ep-pace-event-", "client-pace-event-"}:
            return "JSON Topics: ep-pace-event-, client-pace-event-", None
        return "other JSON topic shape", None
    if data[:1] != b"P":
        raise ValueError("unknown telemetry prefix")
    envelope = list(fields(data[1:]))
    topic = next((value.decode("ascii") for n, w, value in envelope if n == 2 and w == 2), "protobuf without topic")
    if topic not in {"ap-stats-full-", "ap-stats-fast-"}:
        topic = "other protobuf topic"
    stats = next((value for n, w, value in envelope if n == 1 and w == 2), None)
    return topic, stats


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("capture_dir", type=Path)
    parser.add_argument("--nested", default="32,33,36", help="AP stats message field numbers to inventory")
    args = parser.parse_args()
    nested = {int(part) for part in args.nested.split(",") if part}
    topics = Counter()
    frames = Counter()
    occurrences = Counter()
    presence = Counter()
    errors = Counter()
    for path in sorted(args.capture_dir.glob("*.bin")):
        try:
            body = telemetry_body(path.read_bytes())
            if body is None:
                continue
            topic, stats = unpack(body)
            topics[topic] += 1
            if stats is None:
                continue
            frames[topic] += 1
            seen = set()
            for number, wire, value in fields(stats):
                key = (topic, str(number), wire)
                occurrences[key] += 1
                seen.add(key)
                if number in nested and wire == 2:
                    for child_number, child_wire, _ in fields(value):
                        child = (topic, f"{number}.{child_number}", child_wire)
                        occurrences[child] += 1
                        seen.add(child)
            presence.update(seen)
        except (ValueError, OSError, UnicodeError, json.JSONDecodeError):
            errors["unparsed telemetry frames"] += 1
    print("topics (frames):")
    for topic, count in sorted(topics.items()):
        print(f"  {topic}: {count}")
    if errors:
        print(f"unparsed telemetry frames: {sum(errors.values())}")
    print("field inventory: topic path wire frames_with_field/frames occurrences")
    for (topic, path, wire), count in sorted(occurrences.items(), key=lambda pair: (pair[0][0], tuple(map(int, pair[0][1].split("."))), pair[0][2])):
        print(f"  {topic} {path} {wire} {presence[(topic, path, wire)]}/{frames[topic]} {count}")


if __name__ == "__main__":
    main()
