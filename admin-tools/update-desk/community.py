"""What one community runs, from its overlay and the base commit it names.

A change on main matters to a community only when it touches something that
community deploys. That is decided per Application and per tenant folder, not
per unit: an overlay that keeps its object storage elsewhere deletes the Garage
Applications, and a Gallery community builds on tenants/gallery (which itself
builds on tenants/immich), never on tenants/immich alone.
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field

import pins
import units as units_module
from basegit import BaseRepo

K8S = "infrastructure/kubernetes/"
_ROOT_APP = re.compile(r"^\s*-\s*apps/([\w.-]+)\.yaml\s*$", re.M)
_APP_PATH = re.compile(r"^\s*path:\s*['\"]?infrastructure/kubernetes/tenants/([\w.-]+)", re.M)
_SIBLING = re.compile(r"^\s*-\s*\.\./([\w.-]+)/?\s*$", re.M)
DEFAULT_DOMAIN = "smallworlds.network"


@dataclass
class Community:
    applications: set[str] = field(default_factory=set)
    tenants: set[str] = field(default_factory=set)
    units: set[str] = field(default_factory=set)
    domain: str = DEFAULT_DOMAIN

    def relevant(self, path: str, catalog: units_module.Catalog) -> bool:
        """Does a change to this file of the base reach this community?"""
        unit = catalog.unit_of_path(path)
        if unit is None or unit not in self.units:
            return False
        if tenant := catalog.tenant_of_path(path):
            return tenant in self.tenants
        if path.startswith(K8S + "apps/"):
            return path[len(K8S + "apps/"):-len(".yaml")] in self.applications
        return True


def applications_at(base: BaseRepo, sha: str, overlay: pins.Overlay) -> set[str]:
    root = base.show(sha, K8S + "kustomization.yaml") or ""
    names = set(_ROOT_APP.findall(root)) | set(overlay.raw_apps)
    return names - overlay.deleted


def tenants_of(base: BaseRepo, sha: str, overlay: pins.Overlay, applications: set[str],
               catalog: units_module.Catalog) -> set[str]:
    tenants = set()
    for application in applications:
        if application in overlay.routed:
            tenants.update(overlay.folder_tenants.get(overlay.routed[application], []))
            continue
        text = base.show(sha, f"{K8S}apps/{application}.yaml") or ""
        tenants.update(_APP_PATH.findall(text))
    # A tenant that builds on a sibling (gallery on ../immich) runs both.
    queue = list(tenants)
    while queue:
        tenant = queue.pop()
        text = base.show(sha, f"{K8S}tenants/{tenant}/kustomization.yaml") or ""
        for sibling in _SIBLING.findall(text):
            if sibling not in tenants:
                tenants.add(sibling)
                queue.append(sibling)
    return {t for t in tenants if t not in catalog.ring0_tenants}


def at(base: BaseRepo, shas: list[str], overlay: pins.Overlay,
       catalog: units_module.Catalog) -> Community:
    """The community as it stands at any of these commits (their union: a
    tenant that one of them runs is a tenant whose change matters)."""
    result = Community(domain=overlay.domain or DEFAULT_DOMAIN)
    for sha in shas:
        applications = applications_at(base, sha, overlay)
        result.applications |= applications
        result.tenants |= tenants_of(base, sha, overlay, applications, catalog)
    result.applications -= catalog.ring0_applications
    result.units = {catalog.unit_of_application(a) for a in result.applications} - {None}
    result.units |= {catalog.unit_of_tenant(t) for t in result.tenants} - {None}
    result.units.add(units_module.PLATFORM)
    return result
