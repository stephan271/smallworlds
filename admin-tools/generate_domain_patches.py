#!/usr/bin/env python3
import argparse
import os
import textwrap

def generate_patches(app_name, domain, ext):
    """Generate Kustomize patches for a specific app based on the domain and extension."""
    patches = ""
    helm_charts = ""
    
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
        patches += textwrap.dedent(f"""\
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
        """)
    
    elif app_name == "keycloak":
        patches += textwrap.dedent(f"""\
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
        """)
        # Keycloak Helm values patch
        helm_charts += textwrap.dedent(f"""\
        helmCharts:
          - name: keycloakx
            releaseName: keycloak
            valuesInline:
              keycloak:
                extraArgs:
                  - "--hostname={subdomains['identity']}"
        """)

    elif app_name == "stalwart":
        patches += textwrap.dedent(f"""\
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
        """)

    elif app_name == "bulwark":
        patches += textwrap.dedent(f"""\
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
        """)
        
    elif app_name == "nextcloud":
        patches += textwrap.dedent(f"""\
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
        """)
        helm_charts += textwrap.dedent(f"""\
        helmCharts:
          - name: nextcloud
            releaseName: nextcloud
            valuesInline:
              nextcloud:
                host: {subdomains['files']}
              ingress:
                enabled: false
        """)

    elif app_name == "immich":
        patches += textwrap.dedent(f"""\
          - target:
              kind: Job
              name: immich-admin-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: "https://{subdomains['identity']}/realms/smallworlds"
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['photos']}/auth/login", "https://{subdomains['photos']}/user-settings", "app.immich:///"]'
        """)
        helm_charts += textwrap.dedent(f"""\
        helmCharts:
          - name: immich
            releaseName: immich
            valuesInline:
              ingress:
                main:
                  hosts:
                    - host: {subdomains['photos']}
                      paths:
                        - path: "/"
                  tls:
                    - secretName: immich-tls
                      hosts:
                        - {subdomains['photos']}
        """)
        
    elif app_name == "forgejo":
        patches += textwrap.dedent(f"""\
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
        """)
        helm_charts += textwrap.dedent(f"""\
        helmCharts:
          - name: forgejo
            releaseName: forgejo
            valuesInline:
              ingress:
                hosts:
                  - host: {subdomains['git']}
                    paths:
                      - path: /
                        pathType: Prefix
                tls:
                  - hosts:
                      - {subdomains['git']}
                    secretName: forgejo-tls
              gitea:
                config:
                  server:
                    DOMAIN: {subdomains['git']}
                    ROOT_URL: "https://{subdomains['git']}/"
                    SSH_DOMAIN: {subdomains['git']}
        """)

    elif app_name == "jitsi":
        patches += textwrap.dedent(f"""\
          - target:
              kind: Job
              name: keycloak-client-init
            patch: |-
              - op: replace
                path: /spec/template/spec/containers/0/env/0/value
                value: '["https://{subdomains['meet']}/*"]'
        """)
        helm_charts += textwrap.dedent(f"""\
        helmCharts:
          - name: jitsi-meet
            releaseName: jitsi
            valuesInline:
              publicURL: "https://{subdomains['meet']}"
              web:
                ingress:
                  hosts:
                    - host: {subdomains['meet']}
                  tls:
                    - hosts:
                      - {subdomains['meet']}
                      secretName: jitsi-tls
                extraEnvs:
                  TOKEN_AUTH_URL: "https://{subdomains['meet']}/oidc/auth?state={{state}}"
              jwt-app:
                extraEnvs:
                  OIDC_ISSUER: "https://{subdomains['identity']}/realms/smallworlds"
                  JWT_APP_URL: "https://{subdomains['meet']}"
        """)

    elif app_name == "collabora":
        patches += textwrap.dedent(f"""\
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
        """)

    elif app_name == "excalidraw":
        patches += textwrap.dedent(f"""\
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
        """)
        
    elif app_name == "plane":
        helm_charts += textwrap.dedent(f"""\
        helmCharts:
          - name: plane-ce
            releaseName: plane
            valuesInline:
              ingress:
                hosts:
                  - host: {subdomains['plan']}
                    paths:
                      - path: /
                        pathType: ImplementationSpecific
                tls:
                  - hosts:
                    - {subdomains['plan']}
                    secretName: plane-tls
              env:
                NEXT_PUBLIC_API_BASE_URL: "https://{subdomains['plan']}"
        """)

    return patches, helm_charts

def main():
    parser = argparse.ArgumentParser(description="Generate Kustomize domain patches for an overlay repository.")
    parser.add_argument("--app", required=True, help="The application name (e.g. dashboard)")
    parser.add_argument("--domain", required=True, help="The target domain (e.g. example.com)")
    parser.add_argument("--ext", required=False, default="", help="The environment extension (e.g. -dev)")
    parser.add_argument("--kustomization-file", required=True, help="Path to the kustomization.yaml to append patches to")
    
    args = parser.parse_args()
    
    if args.domain == "smallworlds.network" and not args.ext:
        return
        
    patches, helm_charts = generate_patches(args.app, args.domain, args.ext)
    
    if patches or helm_charts:
        with open(getattr(args, 'kustomization_file'), 'a') as f:
            if patches:
                with open(getattr(args, 'kustomization_file'), 'r') as r:
                    content = r.read()
                    if "patches:" not in content:
                        f.write("\npatches:\n")
                f.write(patches)
            if helm_charts:
                f.write("\n" + helm_charts)
            print(f"Appended domain patches for {args.app} to {getattr(args, 'kustomization_file')}")

if __name__ == "__main__":
    main()
