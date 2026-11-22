"""Update Units: which application a file of the base repository belongs to.

Read from infrastructure/kubernetes/update-units.yaml at the commit being looked
at, so a unit added later is known from the commit that adds it.
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field

import miniyaml

PLATFORM = "platform"
K8S = "infrastructure/kubernetes/"
UNITS_FILE = K8S + "update-units.yaml"

_TENANT = re.compile(r"^infrastructure/kubernetes/tenants/([^/]+)/(.*)$")
_APP = re.compile(r"^infrastructure/kubernetes/apps/([^/]+)\.yaml$")
# Files a node reads when it is built or rebuilt; nothing in a running cluster.
NODE_PATHS = ("infrastructure/cloud-init/", "infrastructure/bootstrap-pins.env",
              "infrastructure/local/", "infrastructure/terraform/",
              "infrastructure/golden-image/")


@dataclass
class Unit:
    name: str
    title: str
    applications: list[str] = field(default_factory=list)
    tenants: list[str] = field(default_factory=list)
    migrates: list[str] = field(default_factory=list)
    paths: list[str] = field(default_factory=list)


@dataclass
class Catalog:
    units: dict[str, Unit]
    ring0_applications: set[str]
    ring0_tenants: set[str]
    not_applied: set[str]

    @classmethod
    def parse(cls, text: str) -> "Catalog":
        data = miniyaml.loads(text) or {}
        units = {}
        for entry in data.get("units") or []:
            unit = Unit(name=entry["name"], title=entry.get("title") or entry["name"],
                        applications=list(entry.get("applications") or []),
                        tenants=list(entry.get("tenants") or []),
                        migrates=list(entry.get("migrates") or []),
                        paths=list(entry.get("paths") or []))
            units[unit.name] = unit
        platform = data.get("platform") or {}
        units[PLATFORM] = Unit(name=PLATFORM, title=platform.get("title") or "Platform",
                               migrates=list(platform.get("migrates") or []))
        ring0 = data.get("ring0_only") or {}
        return cls(units=units,
                   ring0_applications=set(ring0.get("applications") or []),
                   ring0_tenants=set(ring0.get("tenants") or []),
                   not_applied=set(data.get("not_applied") or []))

    def unit_of_tenant(self, tenant: str) -> str | None:
        if tenant in self.ring0_tenants:
            return None
        for unit in self.units.values():
            if tenant in unit.tenants:
                return unit.name
        return PLATFORM

    def unit_of_application(self, application: str) -> str | None:
        if application in self.ring0_applications:
            return None
        for unit in self.units.values():
            if application in unit.applications:
                return unit.name
        return PLATFORM

    def unit_of_path(self, path: str) -> str | None:
        """The unit a changed file belongs to; None when no community cluster
        applies the file (docs, scripts, tests, Ring 0's own machinery)."""
        if not path.startswith(K8S) or path in self.not_applied:
            return None
        if path.endswith(".md"):
            return None
        for unit in self.units.values():
            if any(path.startswith(prefix) for prefix in unit.paths):
                return unit.name
        if m := _TENANT.match(path):
            if m.group(2).startswith("tests/") or "/tests/" in m.group(2):
                return None
            return self.unit_of_tenant(m.group(1))
        if m := _APP.match(path):
            return self.unit_of_application(m.group(1))
        return PLATFORM

    def tenant_of_path(self, path: str) -> str | None:
        m = _TENANT.match(path)
        return m.group(1) if m else None


def touches_node(path: str) -> bool:
    return path.startswith(NODE_PATHS)
