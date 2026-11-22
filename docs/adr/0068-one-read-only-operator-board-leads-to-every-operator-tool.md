---
status: proposed
---

# One read-only Operator Board leads to every operator tool

Every cluster runs an **Operator Board**: one page on the Private Network
(`ops.<domain>`) that says in one line whether the cluster is well, shows why
not when it is not, and links to each operator tool where the work is done:
Argo CD, Grafana, the Keycloak admin console, the Invitation Desk, the alert
receivers, the Ring 0 Board and the Update Desk. It is a new Platform Service,
`tenants/operator-board`, built like `join` and the Canary Follower: Python
standard library, source from a ConfigMap, no image build. This answers the
question `docs/adr/0051` and `docs/adr/0062` left open, whether the Operator
Console's observation half returns in a smaller form. It does, and it is not
that code. See `plans-and-walkthroughs/design-operator-board.md` for the
design and phasing.

The decision rests on six judgements.

**It reads and links. It never acts.** Every change to the cluster already has
a narrow tool that does it well and shows what it did: Argo CD syncs, the
Invitation Desk invites, the Update Desk updates, `kubectl` does the rest. The
console's weight was its workflow engine (plan, approve, execute, verify), and a
page with no buttons needs none of it. It needs no confirmation flow and no
activity record. It also cannot be the way an intruder changes the cluster. The
Board links into the tool, onto the object in question when the tool allows it
(the unhealthy Application in Argo CD, the firing alert in Grafana).

**Red means an alert fires.** The Board does not judge health with rules of
its own. The verdict comes from Alertmanager's firing alerts, which already
cover backups, WAL archiving, disks, Argo CD health and the pod devices. They
reach the Operator's phone through ntfy (`doc/alerting.md`), so the Board and
the phone agree. What the Board reads directly (Applications, the node,
backups, certificates, the update position) it shows as facts: amber while
something has not settled, never red on its own. When the Operator finds that
something shown amber deserved red, the fix is a new alert rule, which the phone
gets too, not a rule in the Board.

**Unknown is never green.** Each source (the Kubernetes API, Alertmanager,
Prometheus, `ring0.json`) is reported as answered or not, with the error and the
time of the last good answer. A source that did not answer greys its tiles out
and says so in the verdict. The installer's capacity scan and the Ring 0 Board's
freshness check learned the same lesson: a lookup that failed must not look like
"nothing wrong".

**It runs in the cluster, and says so.** Unlike the Update Desk
(`docs/adr/0067`), the Board needs nothing of the Operator's: no checkout, no
kubeconfig, no token. It reads with a ServiceAccount of its own that may get
and list (no Secrets, no logs, no exec, no writes), so it can stay open in a
browser tab. It cannot report a cluster that is down. The healthchecks.io
heartbeat covers that, and the Board's header says when the cluster last
answered, so a tab that stopped refreshing does not stay green.

**It has no sign-in of its own.** It is restricted the way Argo CD and Grafana
are: a router on the `operators` entrypoint and a LAN-only partner on
`websecure` (`docs/adr/0066`, `doc/hardening.md`). A Keycloak sign-in would
make the Board unreachable exactly when Keycloak is what broke. What it shows
is the class of information Ring 0 already publishes about itself in
`ring0.json`: Argo CD's status per Application, alert names and summaries,
backup times. No Secret, no log line, no member's name. That is what makes the
missing sign-in acceptable, and the design keeps it that way. The known gap
that any pod can reach the `operators` entrypoint (`doc/hardening.md` §1)
applies to the Board as to Grafana.

**It runs on every cluster, Ring 0 included.** Code that is deployed nowhere
rots, as the console did (`docs/adr/0062`). The Board is deployed on every
cluster, in sync wave 0, because it needs no Keycloak and must come up before
the applications it watches. Ring 0 runs it too, and its e2e suite checks it on
every commit. A change that breaks the Board stops the queue like any other
change.

## Consequences

- `ops` is a new core subdomain. It goes into the four subdomain maps, both
  CoreDNS host lists, the hosts snippet and `setup-vpn.sh`'s operator hosts
  line, and a manual-DNS install prints its record. Existing communities add it
  to their Operator Device's hosts line. That is an entry in
  `infrastructure/kubernetes/operator-steps.yaml` in the PR that adds the
  tenant.
- Operator interfaces say how they appear on the Board with annotations on
  their `-private` Ingress (`smallworlds.network/board-*`). The Board finds them
  the way Homepage finds member applications, so a tool that is not installed
  does not appear, and host patches from `generate_domain_patches.py` carry
  over by themselves. A new operator interface adds the annotations along with
  its router pair.
- The Member Dashboard does not change. It stays a page for members and does not
  link to the Board (`doc/hardening.md`).
- The Board reads `ring0.json` and, for a commit older than its history, GitHub's
  public compare API without a token. That sends the cluster's address to
  GitHub, as Argo CD's fetches already do. It can be turned off, and the Updates
  tile then says "unknown".
- The in-cluster Update Desk of `docs/adr/0065`, if it is built, becomes one more
  link. Until then the Updates tile shows where this cluster stands against
  Ring 0 and the command that starts the local Update Desk.
- Not decided: one Board for several clusters (Ring 0 next to Ring 1), and
  anything that needs a credential the cluster does not hold today, such as
  healthchecks.io's own view of the heartbeat.
