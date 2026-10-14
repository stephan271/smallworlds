#!/usr/bin/env python3
import argparse
import re
import textwrap

OFFICE_BACKENDS = ("none", "collabora", "euro-office")


def generate_office_backend_patch(backend):
    """Select exactly one Nextcloud editor, independently of the domain.

    The base defaults to Collabora so old overlays retain their historical
    behaviour. Every newly generated overlay carries an explicit value,
    including ``none``, so later releases cannot silently change that choice.
    """
    if backend not in OFFICE_BACKENDS:
        raise ValueError(f"unsupported office backend: {backend}")
    return textwrap.indent(textwrap.dedent(f"""\
      - target:
          kind: ConfigMap
          name: nextcloud-oidc-config-map
        patch: |-
          - op: replace
            path: /data/OFFICE_BACKEND
            value: "{backend}"
    """), "  ")


def generate_patches(app_name, domain, ext):
    """Generate Kustomize patches for a specific app based on the domain and extension."""
    patches = ""
    
    # Define subdomains
    subdomains = {
        'dashboard': f"dashboard{ext}.{domain}",
        'identity': f"identity{ext}.{domain}",
        'files': f"files{ext}.{domain}",
        'photos': f"photos{ext}.{domain}",
        'git': f"git{ext}.{domain}",
        'mail': f"mail{ext}.{domain}",
        'webmail': f"webmail{ext}.{domain}",
        'monitoring': f"monitoring{ext}.{domain}",
        'whiteboard': f"whiteboard{ext}.{domain}",
        'meet': f"meet{ext}.{domain}",
        'office': f"office{ext}.{domain}",
        'plan': f"plan{ext}.{domain}",
        'deploy': f"deploy{ext}.{domain}",
        'vpn': f"vpn{ext}.{domain}",
        'console': f"console{ext}.{domain}",
        'join': f"join{ext}.{domain}",
        'pod': f"pod{ext}.{domain}"
    }

    if app_name == "headscale":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: headscale
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['vpn']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['vpn']}
          - target:
              kind: Deployment
              name: headscale
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: https://{subdomains['vpn']}
        """), "  ")

    elif app_name == "dashboard":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: dashboard
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['dashboard']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['dashboard']}
        """), "  ")
    
    elif app_name == "keycloak":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: keycloak-keycloakx
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['identity']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['identity']}
          - target:
              kind: Ingress
              name: keycloak-admin
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['identity']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['identity']}
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
                            value: "https://{subdomains['identity']}"
          - target:
              kind: Middleware
              name: keycloak-redirect
            patch: |-
              - op: replace
                path: /spec/redirectRegex/regex
                value: "^https://{subdomains['identity']}/?$"
              - op: replace
                path: /spec/redirectRegex/replacement
                value: "https://{subdomains['identity']}/realms/smallworlds/account/"
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
                            value: "{subdomains['identity']}"
        """), "  ")

    elif app_name == "stalwart":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: stalwart-ingress
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['mail']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['mail']}
          - target:
              kind: Middleware
              name: stalwart-cors
            patch: |-
              - op: replace
                path: /spec/headers/accessControlAllowOriginList/0
                value: "https://{subdomains['webmail']}"
        """), "  ")

    elif app_name == "join":
        # Both Ingresses share one host and one certificate; the ConfigMap names
        # the hosts the join page links to and the issuer every minted link
        # must carry; the two Jobs register the redirects Keycloak checks.
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: join
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['join']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['join']}
          - target:
              kind: Ingress
              name: join-desk
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['join']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['join']}
          - target:
              kind: ConfigMap
              name: join-config
            patch: |-
              - op: replace
                path: /data/PUBLIC_IDENTITY_URL
                value: https://{subdomains['identity']}
              - op: replace
                path: /data/JOIN_URL
                value: https://{subdomains['join']}
              - op: replace
                path: /data/DASHBOARD_URL
                value: https://{subdomains['dashboard']}
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['join']}/*"]'
          - target:
              kind: Job
              name: join-keycloak-setup
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: {subdomains['join']}
        """), "  ")

    elif app_name == "bulwark":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: bulwark-ingress
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['webmail']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['webmail']}
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
                            value: "https://{subdomains['mail']}"
                          - name: OAUTH_ISSUER_URL
                            value: "https://{subdomains['identity']}/realms/smallworlds"
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['webmail']}/*"]'
        """), "  ")
        
    elif app_name == "nextcloud":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: nextcloud
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['files']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['files']}
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
                        value: {subdomains['files']}
                      startupProbe:
                        httpGet:
                          httpHeaders:
                          - name: Host
                            value: {subdomains['files']}
                      livenessProbe:
                        httpGet:
                          httpHeaders:
                          - name: Host
                            value: {subdomains['files']}
                      readinessProbe:
                        httpGet:
                          httpHeaders:
                          - name: Host
                            value: {subdomains['files']}
          - target:
              kind: ConfigMap
              name: nextcloud-oidc-config-map
            patch: |-
              - op: replace
                path: /data/IDENTITY_URL
                value: "https://{subdomains['identity']}/realms/smallworlds"
              - op: replace
                path: /data/FILES_DOMAIN
                value: "{subdomains['files']}"
              - op: replace
                path: /data/OFFICE_URL
                value: "https://{subdomains['office']}"
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['files']}/*"]'
        """), "  ")

    elif app_name == "immich":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: ConfigMap
              name: immich-admin-config
            patch: |-
              - op: replace
                path: /data/ISSUER_URL
                value: "https://{subdomains['identity']}/realms/smallworlds"
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
                            value: '["https://{subdomains['photos']}/auth/login", "https://{subdomains['photos']}/user-settings", "app.immich:///oauth-callback"]'
          - target:
              kind: Ingress
              name: immich-server
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['photos']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['photos']}
        """), "  ")
        
    elif app_name == "forgejo":
        # NOTE: Forgejo's OIDC discovery is bootstrapped by the forgejo-oidc-config
        # Job against Keycloak's in-cluster service URL, so there is no per-domain
        # discovery URL to patch here. Keycloak advertises its public issuer in the
        # discovery document, which comes from KC_HOSTNAME (patched in the keycloak
        # block above) — so fixing the domain there is what makes Forgejo's login
        # redirect to identity{ext}.{domain} instead of the base identity host.
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['git']}/user/oauth2/smallworlds/callback"]'
          - target:
              kind: Ingress
              name: forgejo
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['git']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['git']}
          - target:
              kind: Deployment
              name: forgejo
            patch: |-
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: GITEA__server__DOMAIN
                  value: {subdomains['git']}
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: GITEA__server__ROOT_URL
                  value: "https://{subdomains['git']}/"
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: GITEA__server__SSH_DOMAIN
                  value: {subdomains['git']}
        """), "  ")

    elif app_name == "jitsi":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['meet']}/*"]'
          - target:
              kind: Ingress
              name: jitsi-jitsi-meet-web
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['meet']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['meet']}
          - target:
              kind: ConfigMap
              name: jitsi-jitsi-meet-common
            patch: |-
              - op: replace
                path: /data/PUBLIC_URL
                value: "https://{subdomains['meet']}"
              - op: replace
                path: /data/TOKEN_AUTH_URL
                value: "https://{subdomains['meet']}/oidc/auth?state={{state}}"
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
                            value: "https://{subdomains['identity']}/realms/smallworlds"
                          - name: URL_BASE
                            value: "https://{subdomains['meet']}"
        """), "  ")

    elif app_name == "collabora":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: collabora
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['office']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['office']}
          - target:
              kind: Deployment
              name: collabora
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: "https://{subdomains['files']},https://{subdomains['files']}:443"
              - op: replace
                path: /spec/template/spec/containers/0/env/1/value
                value: {subdomains['office']}
        """), "  ")

    elif app_name == "euro-office":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: euro-office
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['office']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['office']}
        """), "  ")

    elif app_name == "excalidraw":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: excalidraw
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['whiteboard']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['whiteboard']}
        """), "  ")
        
    elif app_name == "plane":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: plane-ingress
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['plan']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['plan']}
          - target:
              kind: ConfigMap
              name: plane-app-vars
            patch: |-
              - op: replace
                path: /data/WEB_URL
                value: "https://{subdomains['plan']}"
        """), "  ")

    elif app_name == "pod-gateway":
        # Only the Ingress: the one in-cluster client, immich-pod-export, reaches
        # the gateway through its Service and never through this hostname. What
        # this address serves is members' devices, which are outside the cluster
        # and pull with a token scoped to a single pod.
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: pod-gateway
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['pod']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['pod']}
        """), "  ")

    elif app_name == "argocd":
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: argocd-server
              namespace: argocd
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['deploy']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['deploy']}
        """), "  ")

    elif app_name == "operator-console":
        # The console has to be told its own address, not just routed to it: the
        # OIDC redirect URI it sends to Keycloak, the redirect URI Keycloak is
        # willing to accept, and the hostname on the Ingress are three separate
        # places that must agree, and a login fails outright if any one of them
        # still names somebody else's domain.
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Ingress
              name: operator-console
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['console']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['console']}
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
                            value: "https://{subdomains['console']}"
                          - name: SMALLWORLDS_OIDC_ISSUER
                            value: "https://{subdomains['identity']}/realms/smallworlds"
                          - name: SMALLWORLDS_BASE_DOMAIN
                            value: "{domain}"
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
                            value: '["https://{subdomains['console']}/api/v1/auth/callback"]'
        """), "  ")

    elif app_name == "monitoring":
        # Modify the values within the kube-prometheus-stack Application manifest for Grafana ingress
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
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
                  # no endpoints, so up{{job=...}} is entirely absent and the absent()-based
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
                        - {subdomains['monitoring']}
                      tls:
                        - secretName: grafana-tls
                          hosts:
                            - {subdomains['monitoring']}
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
        """), "  ")

    return patches

def main():
    parser = argparse.ArgumentParser(description="Generate Kustomize domain patches for an overlay repository.")
    parser.add_argument("--app", required=True, help="The application name (e.g. dashboard)")
    parser.add_argument("--domain", required=True, help="The target domain (e.g. example.com)")
    parser.add_argument("--ext", required=False, default="", help="The environment extension in subdomain syntax (e.g. .dev)")
    parser.add_argument("--kustomization-file", required=True, help="Path to the kustomization.yaml to append patches to")
    parser.add_argument("--office-backend", choices=OFFICE_BACKENDS,
                        help="Explicit Nextcloud editor choice (only valid for --app nextcloud)")
    
    args = parser.parse_args()
    
    if args.office_backend and args.app != "nextcloud":
        parser.error("--office-backend is only valid with --app nextcloud")

    patches = ""
    if args.domain != "smallworlds.network" or args.ext:
        patches = generate_patches(args.app, args.domain, args.ext)
    if args.office_backend:
        # A setup rerun changes the existing choice in place. Appending another
        # JSON patch for the same key happens to be last-one-wins, but leaves an
        # ambiguous overlay and makes every subsequent run noisier.
        with open(args.kustomization_file, "r", newline="\n") as existing:
            content = existing.read()
        office_value = re.compile(
            r"(?m)^(\s*path: /data/OFFICE_BACKEND\n\s*value: )[\"']?[^\"'\n]+[\"']?$"
        )
        updated, replacements = office_value.subn(
            lambda match: f'{match.group(1)}"{args.office_backend}"', content
        )
        if replacements:
            with open(args.kustomization_file, "w", newline="\n") as target:
                target.write(updated)
        else:
            patches += generate_office_backend_patch(args.office_backend)
    
    if patches:
        # newline="\n" on both handles: text mode would translate on Windows, and
        # a kustomization with CRLF endings is not the file the console's own
        # renderer produces for the same overlay. The parity test compares the
        # two byte for byte, and was failing on Windows for exactly this.
        with open(getattr(args, 'kustomization_file'), 'a', newline="\n") as f:
            with open(getattr(args, 'kustomization_file'), 'r', newline="\n") as r:
                content = r.read()
                if "\npatches:" not in content:
                    f.write("\npatches:\n")
            f.write(patches)
        print(f"Appended patches for {args.app} to {getattr(args, 'kustomization_file')}")
    elif args.office_backend:
        print(f"Updated Office backend for {args.app} in {args.kustomization_file}")

if __name__ == "__main__":
    main()
