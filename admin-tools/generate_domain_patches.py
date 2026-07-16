#!/usr/bin/env python3
import argparse
import os
import textwrap

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
        'deploy': f"deploy{ext}.{domain}"
    }

    if app_name == "dashboard":
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
              name: keycloak
            patch: |-
              - op: replace
                path: /spec/rules/0/host
                value: {subdomains['identity']}
              - op: replace
                path: /spec/tls/0/hosts/0
                value: {subdomains['identity']}
          - target:
              kind: StatefulSet
              name: keycloak
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: "https://{subdomains['identity']}"
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
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: "https://{subdomains['mail']}"
              - op: replace
                path: /spec/template/spec/containers/0/env/3/value
                value: "https://{subdomains['identity']}/realms/smallworlds"
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['webmail']}/api/auth/callback"]'
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
              kind: Job
              name: nextcloud-oidc-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/2/value
                value: "https://{subdomains['identity']}/realms/smallworlds"
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['files']}/*"]'
          - target:
              kind: Deployment
              name: nextcloud
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/10/value
                value: {subdomains['files']}
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
                            value: '["https://{subdomains['photos']}/auth/login", "https://{subdomains['photos']}/user-settings", "app.immich:///"]'
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
        patches += textwrap.indent(textwrap.dedent(f"""\
          - target:
              kind: Job
              name: forgejo-oidc-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: "https://{subdomains['identity']}/realms/smallworlds/.well-known/openid-configuration"
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
              kind: Deployment
              name: jitsi-jitsi-meet-web
            patch: |-
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: PUBLIC_URL
                  value: "https://{subdomains['meet']}"
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: TOKEN_AUTH_URL
                  value: "https://{subdomains['meet']}/oidc/auth?state={{state}}"
          - target:
              kind: Deployment
              name: jitsi-jitsi-meet-jwt-app
            patch: |-
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: OIDC_ISSUER
                  value: "https://{subdomains['identity']}/realms/smallworlds"
              - op: add
                path: /spec/template/spec/containers/0/env/-
                value:
                  name: JWT_APP_URL
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
    
    args = parser.parse_args()
    
    if args.domain == "smallworlds.network" and not args.ext:
        return
        
    patches = generate_patches(args.app, args.domain, args.ext)
    
    if patches:
        with open(getattr(args, 'kustomization_file'), 'a') as f:
            with open(getattr(args, 'kustomization_file'), 'r') as r:
                content = r.read()
                if "\npatches:" not in content:
                    f.write("\npatches:\n")
            f.write(patches)
        print(f"Appended domain patches for {args.app} to {getattr(args, 'kustomization_file')}")

if __name__ == "__main__":
    main()
