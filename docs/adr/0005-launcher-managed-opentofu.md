---
status: accepted
---

# Manage a pinned OpenTofu toolchain inside the Bootstrap Launcher

Hetzner infrastructure will continue to be declared in HCL and reconciled with a launcher-managed, pinned OpenTofu toolchain. The launcher will acquire and verify OpenTofu and its providers in private application storage, invoke it behind a narrow Go adapter, and translate plans, progress, and failures into Operator Console concepts; Operators will not install infrastructure tooling separately. Reimplementing infrastructure state management against Hetzner APIs would duplicate mature reconciliation behavior, while requiring an external Terraform installation would violate prerequisite-free onboarding.
