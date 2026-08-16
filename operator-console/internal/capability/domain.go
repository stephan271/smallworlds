package capability

import "strings"

// Hostnames are not derivable from the base manifests: those carry the project's
// own domain, and an overlay that only records the operator's domain somewhere
// leaves every Ingress pointing at smallworlds.network. These patches are what
// actually move a community onto its own domain.
//
// admin-tools/generate_domain_patches.py is the same knowledge for the shell
// path, and domain_parity_test.go compares the two output for output so the pair
// cannot drift apart unnoticed.

// domainPatchTemplates holds one Kustomize patch list per application, already at
// the indentation a `patches:` sequence needs. {{sub}} placeholders are replaced
// with the operator's own hostnames.
var domainPatchTemplates = map[string]string{
	// The coordination server has to be reachable before a device can join the
	// network it coordinates, so it lives on an ordinary Ingress like any other
	// application — and therefore has to follow the operator's domain. Without
	// this entry it kept advertising the project's own vpn.smallworlds.network,
	// which nobody but the project can hold a certificate for.
	"headscale": `  - target:
      kind: Ingress
      name: headscale
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{vpn}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{vpn}}
  - target:
      kind: Deployment
      name: headscale
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: https://{{vpn}}
`,
	// The console has to be told its own address, not just routed to it: the
	// OIDC redirect URI it sends to Keycloak, the redirect URI Keycloak is
	// willing to accept, and the hostname on the Ingress are three separate
	// places that must agree, and a login fails outright if any one of them
	// still names somebody else's domain.
	"operator-console": `  - target:
      kind: Ingress
      name: operator-console
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{console}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{console}}
  - target:
      kind: Deployment
      name: operator-console
    patch: |-
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: operator-console
      spec:
        template:
          spec:
            containers:
              - name: operator-console
                env:
                  - name: SMALLWORLDS_CONSOLE_URL
                    value: "https://{{console}}"
                  - name: SMALLWORLDS_OIDC_ISSUER
                    value: "https://{{identity}}/realms/smallworlds"
                  - name: SMALLWORLDS_BASE_DOMAIN
                    value: "{{domain}}"
  - target:
      kind: Job
      name: keycloak-client-init
      namespace: operator-console
    patch: |-
      apiVersion: batch/v1
      kind: Job
      metadata:
        name: keycloak-client-init
      spec:
        template:
          spec:
            containers:
              - name: setup
                env:
                  - name: REDIRECT_URIS
                    value: '["https://{{console}}/api/v1/auth/callback"]'
`,
	"dashboard": `  - target:
      kind: Ingress
      name: dashboard
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{dashboard}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{dashboard}}
`,
	"keycloak": `  - target:
      kind: Ingress
      name: keycloak-keycloakx
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{identity}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{identity}}
  - target:
      kind: StatefulSet
      name: keycloak-keycloakx
    patch: |-
      apiVersion: apps/v1
      kind: StatefulSet
      metadata:
        name: keycloak-keycloakx
      spec:
        template:
          spec:
            containers:
              - name: keycloak
                env:
                  - name: KC_HOSTNAME
                    value: "https://{{identity}}"
  - target:
      kind: Middleware
      name: keycloak-redirect
    patch: |-
      - op: replace
        path: /spec/redirectRegex/regex
        value: "^https://{{identity}}/?$"
      - op: replace
        path: /spec/redirectRegex/replacement
        value: "https://{{identity}}/realms/smallworlds/account/"
  - target:
      kind: Job
      name: keycloak-realm-config
    patch: |-
      apiVersion: batch/v1
      kind: Job
      metadata:
        name: keycloak-realm-config
      spec:
        template:
          spec:
            containers:
              - name: kcadm
                env:
                  - name: IDENTITY_HOST
                    value: "{{identity}}"
`,
	"stalwart": `  - target:
      kind: Ingress
      name: stalwart-ingress
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{mail}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{mail}}
  - target:
      kind: Middleware
      name: stalwart-cors
    patch: |-
      - op: replace
        path: /spec/headers/accessControlAllowOriginList/0
        value: "https://{{webmail}}"
`,
	"bulwark": `  - target:
      kind: Ingress
      name: bulwark-ingress
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{webmail}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{webmail}}
  - target:
      kind: Deployment
      name: bulwark
    patch: |-
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: bulwark
      spec:
        template:
          spec:
            containers:
              - name: bulwark
                env:
                  - name: JMAP_SERVER_URL
                    value: "https://{{mail}}"
                  - name: OAUTH_ISSUER_URL
                    value: "https://{{identity}}/realms/smallworlds"
  - target:
      kind: Job
      name: keycloak-client-init
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: '["https://{{webmail}}/*"]'
`,
	"nextcloud": `  - target:
      kind: Ingress
      name: nextcloud
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{files}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{files}}
  - target:
      kind: Deployment
      name: nextcloud
    patch: |-
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: nextcloud
      spec:
        template:
          spec:
            containers:
            - name: nextcloud
              env:
              - name: NEXTCLOUD_TRUSTED_DOMAINS
                value: {{files}}
              startupProbe:
                httpGet:
                  httpHeaders:
                  - name: Host
                    value: {{files}}
              livenessProbe:
                httpGet:
                  httpHeaders:
                  - name: Host
                    value: {{files}}
              readinessProbe:
                httpGet:
                  httpHeaders:
                  - name: Host
                    value: {{files}}
  - target:
      kind: ConfigMap
      name: nextcloud-oidc-config-map
    patch: |-
      - op: replace
        path: /data/IDENTITY_URL
        value: "https://{{identity}}/realms/smallworlds"
      - op: replace
        path: /data/FILES_DOMAIN
        value: "{{files}}"
      - op: replace
        path: /data/OFFICE_URL
        value: "https://{{office}}"
  - target:
      kind: Job
      name: keycloak-client-init
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: '["https://{{files}}/*"]'
`,
	"immich": `  - target:
      kind: ConfigMap
      name: immich-admin-config
    patch: |-
      - op: replace
        path: /data/ISSUER_URL
        value: "https://{{identity}}/realms/smallworlds"
  - target:
      kind: Job
      name: keycloak-client-init
    patch: |-
      apiVersion: batch/v1
      kind: Job
      metadata:
        name: keycloak-client-init
      spec:
        template:
          spec:
            containers:
              - name: setup
                env:
                  - name: REDIRECT_URIS
                    value: '["https://{{photos}}/auth/login", "https://{{photos}}/user-settings", "app.immich:///"]'
  - target:
      kind: Ingress
      name: immich-server
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{photos}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{photos}}
`,
	"forgejo": `  - target:
      kind: Job
      name: keycloak-client-init
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: '["https://{{git}}/user/oauth2/smallworlds/callback"]'
  - target:
      kind: Ingress
      name: forgejo
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{git}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{git}}
  - target:
      kind: Deployment
      name: forgejo
    patch: |-
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: GITEA__server__DOMAIN
          value: {{git}}
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: GITEA__server__ROOT_URL
          value: "https://{{git}}/"
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: GITEA__server__SSH_DOMAIN
          value: {{git}}
`,
	"jitsi": `  - target:
      kind: Job
      name: keycloak-client-init
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: '["https://{{meet}}/*"]'
  - target:
      kind: Ingress
      name: jitsi-jitsi-meet-web
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{meet}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{meet}}
  - target:
      kind: ConfigMap
      name: jitsi-jitsi-meet-common
    patch: |-
      - op: replace
        path: /data/PUBLIC_URL
        value: "https://{{meet}}"
      - op: replace
        path: /data/TOKEN_AUTH_URL
        value: "https://{{meet}}/oidc/auth?state={state}"
  # The OIDC adapter runs as an extraContainers sidecar on the web
  # deployment (the old jitsi-jitsi-meet-jwt-app Deployment no longer
  # exists), and the web container itself carries its URLs via the
  # -common ConfigMap (envFrom), not container env. Strategic-merge
  # by container name so the env list merge stays robust.
  - target:
      kind: Deployment
      name: jitsi-jitsi-meet-web
    patch: |-
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: jitsi-jitsi-meet-web
      spec:
        template:
          spec:
            containers:
              - name: jitsi-oidc-adapter
                env:
                  - name: OIDC_ISSUER_URL
                    value: "https://{{identity}}/realms/smallworlds"
                  - name: URL_BASE
                    value: "https://{{meet}}"
`,
	"collabora": `  - target:
      kind: Ingress
      name: collabora
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{office}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{office}}
  - target:
      kind: Deployment
      name: collabora
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: "https://{{files}},https://{{files}}:443"
      - op: replace
        path: /spec/template/spec/containers/0/env/1/value
        value: {{office}}
`,
	"excalidraw": `  - target:
      kind: Ingress
      name: excalidraw
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{whiteboard}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{whiteboard}}
`,
	"plane": `  - target:
      kind: Ingress
      name: plane-ingress
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{plan}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{plan}}
  - target:
      kind: ConfigMap
      name: plane-app-vars
    patch: |-
      - op: replace
        path: /data/WEB_URL
        value: "https://{{plan}}"
`,
	// Only the Ingress: the one in-cluster client, immich-pod-export, reaches the
	// gateway through its Service and never through this hostname. What this
	// address serves is members' devices, which are outside the cluster and pull
	// with a token scoped to a single pod.
	"pod-gateway": `  - target:
      kind: Ingress
      name: pod-gateway
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{pod}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{pod}}
`,
	"argocd-ingress": `  - target:
      kind: Ingress
      name: argocd-server
      namespace: argocd
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: {{deploy}}
      - op: replace
        path: /spec/tls/0/hosts/0
        value: {{deploy}}
`,
	"kube-prometheus-stack": `  - target:
      group: argoproj.io
      kind: Application
      name: kube-prometheus-stack
    patch: |-
      - op: replace
        path: /spec/source/helm/values
        value: |
          # Tier 0 alert suppression (k3s false positives).
          # k3s bundles kube-controller-manager, kube-scheduler and kube-proxy into
          # the single server process and does not expose the per-component metrics
          # endpoints this chart scrapes. The default ServiceMonitors therefore have
          # no endpoints, so up{job=...} is entirely absent and the absent()-based
          # *Down alerts fire as permanent false-positive criticals. Disable both the
          # dead scrape targets (component monitoring) and the corresponding alerting
          # rules. (kube-etcd is left enabled — it does not false-fire here.)
          kubeControllerManager:
            enabled: false
          kubeScheduler:
            enabled: false
          kubeProxy:
            enabled: false
          defaultRules:
            rules:
              # Each of these rule groups contains only the matching *Down alert.
              kubeControllerManager: false
              kubeSchedulerAlerting: false
              kubeProxy: false
          grafana:
            ingress:
              enabled: true
              ingressClassName: traefik
              annotations:
                cert-manager.io/cluster-issuer: letsencrypt-prod
              hosts:
                - {{monitoring}}
              tls:
                - secretName: grafana-tls
                  hosts:
                    - {{monitoring}}
            admin:
              existingSecret: "grafana-admin-creds"
              userKey: admin-user
              passwordKey: admin-password
          prometheus:
            prometheusSpec:
              storageSpec:
                volumeClaimTemplate:
                  spec:
                    accessModes: ["ReadWriteOnce"]
                    resources:
                      requests:
                        storage: 20Gi
          alertmanager:
            alertmanagerSpec:
              # Load AlertmanagerConfig CRs labelled alertmanagerConfig=smallworlds.
              # matcherStrategy None so a single CR in the monitoring namespace routes
              # alerts from ALL namespaces (default OnNamespace would scope it to
              # monitoring only). Routing/receivers live in apps/alertmanager-config.yaml.
              alertmanagerConfigSelector:
                matchLabels:
                  alertmanagerConfig: smallworlds
              alertmanagerConfigMatcherStrategy:
                type: None
              storage:
                volumeClaimTemplate:
                  spec:
                    accessModes: ["ReadWriteOnce"]
                    resources:
                      requests:
                        storage: 2Gi
`,
}

// subdomainNames maps each placeholder to the label placed in front of the
// operator's domain. The extension sits between label and domain, so a .dev
// cluster can never collide with production's hostnames.
var subdomainNames = []string{"dashboard", "identity", "files", "photos", "git", "mail", "webmail", "monitoring", "whiteboard", "meet", "office", "plan", "deploy", "vpn", "console", "pod"}

// DomainPatches returns the Kustomize patch entries that move one application's
// hostnames onto the operator's domain, or "" when the application needs none.
//
// The base manifests already carry the project's own domain, so an overlay for
// smallworlds.network without an extension needs no patches at all — matching
// the shell path, which skips the same case.
func DomainPatches(app, domain, ext string) string {
	if domain == "smallworlds.network" && ext == "" {
		return ""
	}
	template, found := domainPatchTemplates[app]
	if !found {
		return ""
	}
	replacements := make([]string, 0, len(subdomainNames)*2+2)
	for _, name := range subdomainNames {
		replacements = append(replacements, "{{"+name+"}}", name+ext+"."+domain)
	}
	// The bare domain, without a subdomain or the environment extension. The
	// console needs it to build Grafana and Argo CD deep links, which it derives
	// from the base domain rather than being told each hostname separately.
	replacements = append(replacements, "{{domain}}", domain)
	return strings.NewReplacer(replacements...).Replace(template)
}
