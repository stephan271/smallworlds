---
status: accepted
---

# Split authority between the Bootstrap Launcher and the in-cluster console

The Bootstrap Launcher owns infrastructure and node lifecycle, initial Private Network establishment, local credential recovery, and break-glass work because those capabilities must remain available when the cluster is absent or unhealthy. The in-cluster Operator Console owns observation and later bounded day-two operations. Both surfaces share concepts and presentation, but the in-cluster process will not gain self-destruction or external infrastructure authority merely to make the product appear physically singular.
