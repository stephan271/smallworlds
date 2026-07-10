# SmallWorlds Roadmap

This document outlines the upcoming steps and future direction of the SmallWorlds project, separated into Applications and Infrastructure.

## Checklist semantics

- **✅ Initial implementation exists** means a first implementation exists in this repository. It is not a claim that the capability is production-ready, fully documented, or complete.
- **⬜ Planned — no initial implementation yet** means no initial implementation exists yet.

## Applications

The following applications are planned to expand the capabilities of the SmallWorlds sovereign citizen cloud:

### Video Conferencing
- **✅ Initial implementation exists:** Jitsi Meet integration.
- **Recommendation:** **Jitsi Meet**
  - *Why:* Jitsi Meet is lightweight, purely open-source, and integrates beautifully with standard auth setups. It is self-hostable, respects privacy, and requires minimal overhead compared to alternatives like BigBlueButton (which is better suited for structured classroom learning rather than general-purpose video calls). If deep integration with Nextcloud is preferred, Nextcloud Talk is also an option, but Jitsi Meet remains the superior standalone video conferencing tool.

### Drawing and Diagramming
- **✅ Initial implementation exists:** Excalidraw integration.
- **Target:** **Excalidraw**
  - *Details:* A collaborative, virtual whiteboard tool that excels in sketching diagrams and wireframes. It is highly popular due to its intuitive interface and handwritten aesthetic. We will host the Excalidraw Docker container and potentially integrate it with our storage solutions.

### Data Analysis
- **⬜ Planned — no initial implementation yet.**
- **Target:** **JupyterHub**
  - *Details:* Multi-user server for Jupyter notebooks. This will allow users to spin up their own data analysis environments in Python, R, or Julia with persistent storage backends tied to the cluster.

### Publication Editing
- **⬜ Planned — no initial implementation yet.**
- **Target:** **ShareLaTeX / Overleaf Community Edition**
  - *Details:* Real-time collaborative LaTeX editor. Essential for academic and professional publication authoring. The open-source community edition provides a solid foundation for robust document compilation.

### Software Project Management
- **✅ Initial implementation exists:** Plane integration.
- **Recommendation:** **Plane** (previously Taiga)
  - *Why:* Plane is the modern default for self-hosted agile PM: full self-hosting with no user or feature limits in the Community Edition, a rapid development pace, and Scrum/Kanban boards, sprints, user stories, and issues — providing a more comprehensive planning interface than Forgejo's built-in issue tracking. We moved away from **Taiga** because Kaleidos, the company behind it, has wound down; its designated successor **Tenzu** shipped its first stable release in September 2025 but is still far from Taiga's feature parity. If deep waterfall/Gantt planning is ever needed, **OpenProject** is the heavier alternative, but Plane best fits our needs.

### Wiki Documentation
- **⬜ Planned — no initial implementation yet.**
- **Recommendation:** **Outline** (or **Docmost**)
  - *Why:* **Outline** offers an ultra-modern, Notion-like collaborative editing experience and is actively maintained. It depends on PostgreSQL and Redis and requires an OIDC provider like Keycloak — which we already run — making it a natural fit for our stack. **Docmost** is a lighter, very actively developed alternative worth evaluating. We moved away from **Wiki.js**, our earlier pick, because its development has stalled: the v3 rewrite has had "no ETA" since a 2022 developer preview, v2 is in maintenance mode, and the main repository has seen little recent activity, while Outline, Docmost, and BookStack are all actively developed.

### Open Notebook
- **⬜ Planned — no initial implementation yet.**
- **Target:** **Open Notebook** ([github.com/lfnovo/open-notebook](https://github.com/lfnovo/open-notebook))
  - *Details:* An open-source, privacy-focused alternative to Google's Notebook LM. Allows users to organize notes and run local or API-based LLMs against their personal data without sending data to external, non-sovereign clouds.

---

## Social Media

Fact-checking and ethical alignment should be fundamental components of all social media applications within this ecosystem. Unsolicited advertisements must be entirely avoided; instead, users should have full control, actively opting in for advertisements that align with their interests.

Furthermore, all supported social media applications must deliberately avoid the "race to the bottom"—a phenomenon where platforms compete for user engagement at any cost, often sacrificing ethical standards, user well-being, and content quality. This destructive competition has led to the widespread implementation of manipulative features such as:

- Autoplay to keep users passively consuming content
- Infinite scrolling to eliminate natural stopping points
- Emotionally charged content designed to provoke outrage or addiction

To counteract these issues, social media apps in this project must reject these exploitative tactics and prioritize a healthier, more transparent, and user-centric digital experience. The goal is to create platforms that foster meaningful interactions, support informed discourse, and respect users' time and mental well-being.

### Messaging
- **⬜ Planned — no initial implementation yet.**
- **Recommendation:** **Matrix (Synapse server + Element client)**
  - *Details:* While Signal is secure, it relies on centralized servers and phone numbers. Matrix is a fully decentralized, federated standard. By hosting a Matrix homeserver, users truly own their data and conversations. It seamlessly integrates into a self-hosted ecosystem and serves as a privacy-respecting alternative to WhatsApp or Telegram.

### News Sharing and Microblogging
- **⬜ Planned — no initial implementation yet.**
- **Recommendation:** **Mastodon**
  - *Details:* The gold standard for self-hosted, federated microblogging (ActivityPub protocol). Mastodon strictly enforces chronological feeds with no algorithmic manipulation, no ads, and no addictive dark patterns, making it the perfect decentralized alternative to X/Twitter.

### Image Sharing
- **⬜ Planned — no initial implementation yet.**
- **Recommendation:** **Pixelfed**
  - *Details:* A federated image-sharing platform that respects privacy. Like Mastodon, it offers chronological timelines without targeted ads or third-party tracking, acting as a healthy, ethical alternative to Instagram.

### Video Sharing
- **⬜ Planned — no initial implementation yet.**
- **Recommendation:** **PeerTube**
  - *Details:* A federated video streaming platform. It uses peer-to-peer networking (WebTorrent) to share the bandwidth load among viewers, meaning a small, self-hosted server can deliver video effectively without massive infrastructure costs. It replaces YouTube while avoiding autoplay and algorithmic rabbit holes.

---

## Deployment targets

### Local installation
- **⬜ Planned — no initial implementation yet:** support installation on a home server or other on-premises hardware without Hetzner Cloud APIs or infrastructure.

### Other cloud providers
- **⬜ Planned — no initial implementation yet:** support providers beyond Hetzner Cloud, beginning with provider-specific infrastructure, DNS, storage, and bootstrap modules.

## Infrastructure

The vision for SmallWorlds infrastructure is to move towards a fully autonomous, agent-driven system for lifecycle management. Below are the steps to arrive at this state:

### Step 0: Backup functions
- **✅ Initial implementation exists:** scheduled CNPG backups to Garage S3, Velero cluster backups, and the backup-replicator job.
- ensure each application has a scheduled backup based on the garage S3 service
- keep second backup instance on local server at customers home address

### Step 1: GitOps Foundation
- **✅ Initial implementation exists:** Argo CD and Kustomize-based GitOps.
- **Goal:** Ensure all infrastructure components and app configurations are 100% declaratively managed.
- **Action:** Solidify ArgoCD/Flux deployments. Every app (including the new ones listed above) must be deployed via Kustomize or Helm charts driven completely by Git commits. Manual `kubectl` actions should be read-only or strictly temporary.

### Step 2: Observability and Telemetry Integration
- **✅ Initial implementation exists:** Prometheus, Alertmanager, Loki, and Grafana.
- **Goal:** Provide the agent with "senses" to understand cluster health.
- **Action:** Deploy Prometheus, Alertmanager, Loki, and Grafana. Expose structured logs, metrics, and alerts to a unified message bus or API that an AI agent can query and subscribe to.

### Step 3: CI/CD Pipeline as an Agent Interface
- **✅ Initial implementation exists:** staging/E2E runner and Renovate pull-request automation.
- **Goal:** Allow the agent to propose changes safely.
- **Action:** Establish pipelines where the agent can run tests, lint manifests, and create Pull Requests (in Forgejo). Human approval is still required at this stage for the agent's PRs.

### Step 4: The SRE Agent Platform
- **✅ Initial implementation exists:** Hermes agent with observability and cluster integrations.
- **Goal:** Deploy a localized LLM-powered agent to manage operations.
- **Action:** Introduce an agent framework (like the Hermes Agent or an open-source equivalent) that has access to:
  1. The cluster's observability stack (to detect failing pods, OOM errors, or network issues).
  2. The Git repository (to patch configurations, bump image tags, or scale resources).
  3. The Kubernetes API (for real-time safe remediation, like restarting stuck pods).

### Step 5: Autonomous Lifecycle Management
- **✅ Initial implementation exists:** remediation and escalation; fully autonomous lifecycle management remains future work.
- **Goal:** Closed-loop self-healing and continuous updates.
- **Action:** Empower the agent to handle routine maintenance autonomously without human intervention.
  - **App Updates:** The agent detects a new release of Nextcloud, tests the upgrade in a temporary namespace, and automatically pushes the update to production if health checks pass.
  - **Self-Healing:** If the agent detects a database performance bottleneck, it autonomously submits a PR to increase resource limits and applies it.
  - **Status Page Integration:** The agent automatically updates `status.json` dynamically when an application is degraded or under maintenance, immediately reflecting the change on the dashboard.
