"""Kubernetes memory-quantity helpers (bytes <-> Mi/Gi strings)."""
import math
import re

_SUFFIXES = {
    "": 1, "k": 1000, "M": 1000**2, "G": 1000**3, "T": 1000**4,
    "Ki": 1024, "Mi": 1024**2, "Gi": 1024**3, "Ti": 1024**4,
}
_RE = re.compile(r"^\s*([0-9.]+)\s*([A-Za-z]*)\s*$")


def parse(quantity: str) -> float:
    """Parse a k8s memory quantity (e.g. '256Mi', '1Gi', '536870912') to bytes."""
    m = _RE.match(quantity)
    if not m:
        raise ValueError(f"unparseable memory quantity: {quantity!r}")
    value, suffix = m.group(1), m.group(2)
    if suffix not in _SUFFIXES:
        raise ValueError(f"unknown memory suffix: {suffix!r}")
    return float(value) * _SUFFIXES[suffix]


def to_mi(num_bytes: float) -> str:
    """Round up to a whole Mebibyte and format as a k8s quantity string."""
    return f"{math.ceil(num_bytes / (1024 ** 2))}Mi"
