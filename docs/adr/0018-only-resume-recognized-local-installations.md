---
status: accepted
---

# Only resume recognized installations on Local Cluster Nodes

Ordinary Local bootstrap will target a dedicated node. It may resume or reconcile a SmallWorlds installation recognized as belonging to the selected Cluster Profile, while another profile requires an explicit import or profile switch. Unrelated Kubernetes installations and occupied SmallWorlds paths or ports are blockers, and existing persistent data requires a separate inventory and recovery plan. Normal setup will never uninstall foreign software, wipe unidentified data, or adopt unknown workloads.
