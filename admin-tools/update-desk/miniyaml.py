"""A reader for the small YAML subset the Desk's data files use.

update-units.yaml, upgrade-stops.yaml and operator-steps.yaml are written by
people and read here without third-party packages. They use block mappings,
block sequences, inline lists (`[a, b]`), quoted and plain scalars, comments and
block scalars (`|`, `>`, `>-`). Anything else raises ValueError rather than being
read wrong.
"""
from __future__ import annotations

import re

_KEY = re.compile(r"^([A-Za-z0-9_.-]+):(?:\s+(.*))?$")


def _strip_comment(text: str) -> str:
    quote = None
    for index, char in enumerate(text):
        if quote:
            if char == quote:
                quote = None
        elif char in "'\"":
            quote = char
        elif char == "#" and (index == 0 or text[index - 1] in " \t"):
            return text[:index].rstrip()
    return text.rstrip()


def _scalar(text: str):
    text = text.strip()
    if text == "":
        return None
    if text[0] in "'\"" and text[-1] == text[0] and len(text) >= 2:
        return text[1:-1]
    if text.startswith("["):
        if not text.endswith("]"):
            raise ValueError(f"unterminated list: {text}")
        inner = text[1:-1].strip()
        return [] if not inner else [_scalar(item) for item in inner.split(",")]
    if text in ("true", "false"):
        return text == "true"
    if re.fullmatch(r"-?\d+", text):
        return int(text)
    return text


class _Lines:
    def __init__(self, text: str):
        self.raw = text.splitlines()
        self.items = []  # (indent, content, raw index)
        for number, line in enumerate(self.raw):
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            if "\t" in line[: len(line) - len(line.lstrip())]:
                raise ValueError(f"tab indentation on line {number + 1}")
            self.items.append((len(line) - len(line.lstrip()), line.strip(), number))
        self.position = 0

    def peek(self):
        return self.items[self.position] if self.position < len(self.items) else None


def _block_scalar(lines: _Lines, style: str, parent_indent: int, raw_index: int) -> str:
    collected = []
    index = raw_index + 1
    indent = None
    while index < len(lines.raw):
        line = lines.raw[index]
        if line.strip():
            current = len(line) - len(line.lstrip())
            if current <= parent_indent:
                break
            if indent is None:
                indent = current
            collected.append(line[indent:] if len(line) >= indent else line.strip())
        else:
            collected.append("")
        index += 1
    while lines.peek() and lines.peek()[2] < index:
        lines.position += 1
    while collected and collected[-1] == "":
        collected.pop()
    if style.startswith(">"):
        text = " ".join(part.strip() for part in collected if part.strip())
    else:
        text = "\n".join(collected)
    return text if style.endswith("-") else text + "\n"


def _value(lines: _Lines, rest: str | None, indent: int, raw_index: int):
    rest = _strip_comment(rest or "")
    if rest in ("|", "|-", ">", ">-"):
        return _block_scalar(lines, rest, indent, raw_index)
    if rest:
        return _scalar(rest)
    following = lines.peek()
    if following is None or following[0] < indent:
        return None
    if following[0] == indent and not following[1].startswith("- "):
        return None
    return _node(lines, following[0])


def _mapping(lines: _Lines, indent: int, first: str | None = None, first_index: int = 0) -> dict:
    result = {}
    pending = [(first, first_index)] if first is not None else []
    while True:
        if pending:
            content, raw_index = pending.pop()
        else:
            item = lines.peek()
            if item is None or item[0] != indent or item[1].startswith("- "):
                break
            lines.position += 1
            content, raw_index = item[1], item[2]
        match = _KEY.match(_strip_comment(content))
        if not match:
            raise ValueError(f"expected 'key: value' on line {raw_index + 1}: {content}")
        key, rest = match.group(1), match.group(2)
        if key in result:
            raise ValueError(f"duplicate key {key!r} on line {raw_index + 1}")
        result[key] = _value(lines, rest, indent, raw_index)
    return result


def _sequence(lines: _Lines, indent: int) -> list:
    result = []
    while True:
        item = lines.peek()
        if item is None or item[0] != indent or not item[1].startswith("- "):
            break
        lines.position += 1
        content = item[1][2:].strip()
        if _KEY.match(_strip_comment(content)):
            result.append(_mapping(lines, indent + 2, content, item[2]))
        else:
            result.append(_scalar(_strip_comment(content)))
    return result


def _node(lines: _Lines, indent: int):
    item = lines.peek()
    if item[1].startswith("- "):
        return _sequence(lines, indent)
    return _mapping(lines, indent)


def loads(text: str):
    lines = _Lines(text)
    if lines.peek() is None:
        return None
    result = _node(lines, lines.peek()[0])
    if lines.peek() is not None:
        raise ValueError(f"unexpected indentation on line {lines.peek()[2] + 1}")
    return result
