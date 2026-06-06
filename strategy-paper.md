# SmallWorlds: Technical Strategy and Implementation Approach

## 1. Executive Summary
The SmallWorlds project envisions a distributed, decentralized network of community-operated digital hubs ("small worlds") offering a complete suite of privacy-respecting, everyday software applications. The goal is to provide a 99% coverage of normal user needs through fully automated, secure, and fun-to-use platforms. This document outlines the technological strategy to realize this vision, starting with an initial pilot of an Immich server on European cloud infrastructure.

## 2. Core Architectural Principles
To achieve decentralization, high security, and low operational overhead, the system will be built on the following principles:
- **Infrastructure as Code (IaC) & GitOps:** Every configuration, application state, and infrastructure definition will be version-controlled. Changes are made via Git commits, enabling full traceability and automated rollouts.
- **Sovereign Cloud Native:** Hosted on European cloud providers (e.g., Hetzner, Scaleway, Exoscale) to ensure compliance with GDPR and digital sovereignty. 
- **Privacy & Security by Design:** Implementing Zero-Trust network models, automated certificate management (Let's Encrypt), and decentralized identity (OIDC/Passkeys via Keycloak).
- **Modularity:** Applications are decoupled from the underlying hardware, allowing easy migration, backup, and scaling.
- **Isolated Stateful Services (Databases & Caches):** Following a "shared-nothing" architecture, each application will receive its own dedicated transactional database and cache instance (e.g., Immich and Nextcloud will not share a single PostgreSQL cluster). This guarantees robust fault isolation, eliminates version-locking dependencies during upgrades, and limits the blast radius of potential security vulnerabilities.
- **Shared Object Storage (S3):** Unlike transactional databases, the S3 object storage layer (e.g., Garage or MinIO) is designed to be highly scalable, multitenant by default, and stateless from the application's perspective. Applications will share a single robust S3 cluster but will be securely isolated into separate buckets with dedicated access credentials.

## 3. Technology Evaluation for Automation

We will heavily leverage existing, battle-tested Operators for backing services. A prime example is **CloudNativePG**, an operator that fully automates the lifecycle of PostgreSQL. It solves the complex problem of database administration in Kubernetes by automatically handling:
- High Availability (HA) and automated failover
- Streaming replication and point-in-time recovery (PITR) backups
- Seamless, zero-downtime rolling updates

By relying on such Operators, we abstract away the most difficult parts of running a cloud service (the stateful data layer) and ensure enterprise-grade reliability for every isolated application database.
**Recommended Strategy:** 
We will use a **GitOps approach (using ArgoCD or Flux)** to orchestrate the entire "bundle". The GitOps controller will automatically sync the cluster state with a Git repository. We will use existing **Helm Charts** for the applications (like Immich) and rely on **existing Operators** exclusively for backing services (Databases, Storage, Cert-Manager). This provides the "fully automated" experience you want with significantly reduced development overhead.

## 4. Phase 1: The Immich Pilot
To prove the architecture's viability, we will deploy **Immich** (Google Photos alternative) as the initial pilot. Immich is a perfect acid-test because it requires:
- High-performance storage for media (S3-compatible, e.g., Garage/Minio).
- Relational Database (PostgreSQL) and Cache (Redis).
- Machine Learning containers for facial recognition and object detection.
- Mobile client integration.

By successfully automating Immich on a European cloud, we validate the networking, storage, database operators, and GitOps pipeline. 

## 5. Ongoing Automation & Security
- **Updates:** Using tools like *RenovateBot* to automatically create pull requests for software updates, which the GitOps pipeline then automatically deploys.
- **Backups:** Automated, encrypted off-site backups using *Velero* (for cluster state) and *Rclone/Borg* (for user data).
- **Federation:** Once the base node is stable, we will explore federation protocols (ActivityPub, Matrix) to link isolated "small worlds" together, forming the broader CitizenNet.
