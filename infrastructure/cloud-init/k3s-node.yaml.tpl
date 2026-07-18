#cloud-config
# Shared k3s + ArgoCD bootstrap template, consumed by both terraform roots
# (infrastructure/terraform and infrastructure/terraform-staging) via
# templatefile(). Variables:
#
#   golden_image      bool    boot from pre-baked snapshot (k3s + images baked in)
#   node_name         string  k3s --node-name (must be stable across snapshot boots)
#   server_ip         string  static primary IP; "" = discover dynamic IP at runtime
#   domain_name       string  root domain for the CoreDNS override
#   env_ext           string  subdomain-syntax env extension (".dev"); "" for prod/staging
#   acme_email        string  Let's Encrypt account email; "" = self-signed issuer
#   hcloud_token      string  Hetzner Cloud API token, for the cert-manager DNS01
#                             webhook (ACME challenges); "" when acme_email is also ""
#   root_app_git_url  string  overlay repo for the ArgoCD root app; "" = no root app
#                             (the staging pipeline applies Applications itself)
#   persistent_volume bool    mount Hetzner volume + relocate k3s data onto it
%{ if golden_image ~}
# Golden image: packages and updates are baked in
package_update: false
%{ else ~}
package_update: true
packages:
  - curl
  - jq
%{ endif ~}

swap:
  filename: /swap.img
  size: "8G"

write_files:

  - path: /etc/sysctl.d/99-kubernetes-cri.conf
    permissions: '0644'
    content: |
      fs.inotify.max_user_instances=8192
      fs.inotify.max_user_watches=524288
%{ if root_app_git_url != "" }
  - path: /tmp/argocd-root-app.yaml
    permissions: '0644'
    content: |
      apiVersion: argoproj.io/v1alpha1
      kind: Application
      metadata:
        name: smallworlds-root
        namespace: argocd
        finalizers:
          - resources-finalizer.argocd.argoproj.io
      spec:
        project: default
        source:
          repoURL: '${root_app_git_url}'
          targetRevision: HEAD
          path: .
        destination:
          server: 'https://kubernetes.default.svc'
          namespace: argocd
        syncPolicy:
          # Generous retries: without them ArgoCD gives up after 5 attempts and
          # never retries the same revision — one transient wave failure during
          # bootstrap then stalls the whole install until a manual sync
          retry:
            limit: 20
            backoff:
              duration: 15s
              factor: 2
              maxDuration: 5m
          automated:
            prune: true
            selfHeal: true
          syncOptions:
            - CreateNamespace=true
            - SkipDryRunOnMissingResource=true
%{ endif }
runcmd:
%{ if persistent_volume ~}
  # 1. Mount persistent volume
  - |
    VOLUME_DEVICE=$(lsblk -rpo 'NAME,MOUNTPOINT' | awk '$2=="" && $1 !~ /^\/dev\/(sda|sr)/ {print $1}' | head -n1)
    if [ -n "$VOLUME_DEVICE" ]; then
      echo "Found unmounted volume at: $VOLUME_DEVICE"
      MOUNT_DIR=$(lsblk -rpo 'NAME,MOUNTPOINT' | awk -v dev="$VOLUME_DEVICE" '$1==dev {print $2}' | head -n1)
      if [ -z "$MOUNT_DIR" ]; then MOUNT_DIR="/mnt/smallworlds-data" ; mkdir -p $MOUNT_DIR ; mount $VOLUME_DEVICE $MOUNT_DIR ; fi
      ln -sfn $MOUNT_DIR /mnt/smallworlds-data
    else
      # Volume already automounted by Hetzner; find it
      MOUNT_DIR=$(lsblk -rpo 'NAME,MOUNTPOINT' | awk '$2 ~ /^.mnt/ && $2!="/mnt/smallworlds-data" {print $2}' | head -n1)
      if [ -n "$MOUNT_DIR" ]; then ln -sfn $MOUNT_DIR /mnt/smallworlds-data ; fi
    fi
    mkdir -p /mnt/smallworlds-data/garage /mnt/smallworlds-data/immich-library /mnt/smallworlds-data/k3s
%{ else ~}
  # 1. No persistent volume (ephemeral node): plain local directories
  - mkdir -p /mnt/smallworlds-data/garage /mnt/smallworlds-data/immich-library /mnt/smallworlds-data/k3s
%{ endif ~}

  # Purge any cluster state a snapshot image may carry (datastore, TLS, node
  # identity) — a fresh node must never inherit the image builder's cluster.
  # No-op on plain Ubuntu; must run BEFORE the relocate/symlink below so a
  # preserved volume's own server state is untouched.
  - rm -rf /var/lib/rancher/k3s/server /etc/rancher/k3s /etc/rancher/node

%{ if persistent_volume ~}
  # Relocate K3s data to the persistent volume to survive VM deletion
  - if [ -d "/var/lib/rancher/k3s" ] && [ ! -L "/var/lib/rancher/k3s" ]; then cp -a /var/lib/rancher/k3s/* /mnt/smallworlds-data/k3s/ ; rm -rf /var/lib/rancher/k3s ; fi
%{ endif ~}
  - mkdir -p /var/lib/rancher
  - ln -sfn /mnt/smallworlds-data/k3s /var/lib/rancher/k3s

%{ if server_ip != "" ~}
  - NODE_IP=${server_ip}
%{ else ~}
  # No static primary IP: discover the dynamic IP at runtime
  - NODE_IP=$(hostname -I | awk '{print $1}')
%{ endif ~}
  - sysctl --system
  - echo "ipv4" > ~/.curlrc

  # Bootstrap manifests are generated HERE, not via write_files: write_files
  # runs before runcmd, so the purge above would delete anything it placed
  # under /var/lib/rancher/k3s/server. After the symlink this directory lives
  # on the persistent volume (when there is one), so the manifests survive
  # VM re-creation and are refreshed deterministically on every boot.
  - mkdir -p /var/lib/rancher/k3s/server/manifests
%{ if acme_email != "" ~}
  # Existing Let's Encrypt certificates are restored from the operator's
  # laptop after terraform apply (rate-limit protection) — see
  # admin-tools/restore-certs-from-laptop.sh
  - |
    cat > /var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml <<'ISSUER'
    apiVersion: cert-manager.io/v1
    kind: ClusterIssuer
    metadata:
      name: letsencrypt-prod
    spec:
      acme:
        server: https://acme-v02.api.letsencrypt.org/directory
        email: "${acme_email}"
        privateKeySecretRef:
          name: letsencrypt-prod
        solvers:
        - dns01:
            webhook:
              groupName: acme.hetzner.com
              solverName: hetzner
              config:
                tokenSecretKeyRef:
                  name: hetzner
                  key: token
    ISSUER
  # DNS01 challenge token for the cert-manager-webhook-hetzner solver above,
  # written directly rather than via ArgoCD so it exists before the first
  # ACME order fires. k3s's manifest controller retries on apply failure, so
  # this succeeds once the cert-manager Application creates its namespace.
  - |
    cat > /var/lib/rancher/k3s/server/manifests/cert-manager-webhook-hetzner-secret.yaml <<'HETZNERSECRET'
    apiVersion: v1
    kind: Secret
    metadata:
      name: hetzner
      namespace: cert-manager
    type: Opaque
    stringData:
      token: "${hcloud_token}"
    HETZNERSECRET
%{ else ~}
  # Self-signed issuer published under the production name so the
  # cluster-issuer annotations on the Ingresses work unchanged
  - |
    cat > /var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml <<'ISSUER'
    apiVersion: cert-manager.io/v1
    kind: ClusterIssuer
    metadata:
      name: letsencrypt-prod
    spec:
      selfSigned: {}
    ISSUER
%{ endif ~}

  # In-cluster DNS override: without it pods resolve the app domains via
  # public DNS — on an ephemeral staging node that points at the PRODUCTION
  # server, and OIDC token exchanges silently talk to the wrong Keycloak.
  # Generated at runtime because $NODE_IP may be dynamic.
  - |
    cat > /var/lib/rancher/k3s/server/manifests/coredns-custom.yaml <<COREDNS
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: coredns-custom
      namespace: kube-system
    data:
      smallworlds.server: |
        ${domain_name}:53 {
          hosts {
            $NODE_IP identity${env_ext}.${domain_name} files${env_ext}.${domain_name} photos${env_ext}.${domain_name} git${env_ext}.${domain_name} mail${env_ext}.${domain_name} meet${env_ext}.${domain_name} webmail${env_ext}.${domain_name} whiteboard${env_ext}.${domain_name} office${env_ext}.${domain_name} dashboard${env_ext}.${domain_name} monitoring${env_ext}.${domain_name}
            fallthrough
          }
          forward . /etc/resolv.conf
        }
    COREDNS

  # 2. Install K3s (auto-applies the manifests written above)
  # Explicit --node-name: at first boot from a snapshot the transient hostname
  # can still be the image builder's when k3s starts, registering a ghost node
%{ if golden_image ~}
  # Golden image: k3s binary and container images are baked in — just
  # regenerate the systemd unit with node-specific args and start
  - INSTALL_K3S_SKIP_DOWNLOAD=true /usr/local/lib/k3s-install.sh server --cluster-init --node-ip=$NODE_IP --node-name=${node_name} --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
%{ else ~}
  - curl -sfL https://get.k3s.io | sh -s - server --cluster-init --node-ip=$NODE_IP --node-name=${node_name} --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
%{ endif ~}
  - export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
  - until kubectl get nodes | grep -v NotReady | grep -q Ready; do sleep 5; done

  # Export kubeconfig for remote retrieval — the staging pipeline polls for
  # this file as its "cluster is up" signal, then scp's it
  - cp /etc/rancher/k3s/k3s.yaml /root/k3s.yaml
  - sed -i "s/127.0.0.1/$NODE_IP/g" /root/k3s.yaml

  # 3. Install ArgoCD
  - kubectl create namespace argocd || true
  - kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml --server-side --force-conflicts
  - |
    cat << 'EOF' > /tmp/argocd-cm-patch.yaml
    data:
      kustomize.buildOptions: "--enable-helm"
      server.insecure: "true"
      resource.customizations.health.argoproj.io_Application: |
        hs = {}
        hs.status = "Progressing"
        hs.message = ""
        if obj.status ~= nil then
          if obj.status.health ~= nil then
            hs.status = obj.status.health.status
            if obj.status.health.message ~= nil then
              hs.message = obj.status.health.message
            end
          end
        end
        return hs
    EOF
  - kubectl patch cm/argocd-cm -n argocd --type=merge --patch-file /tmp/argocd-cm-patch.yaml
  # server.insecure is only honored in argocd-cmd-params-cm (NOT argocd-cm);
  # without it argocd-server 307-redirects Traefik's plain-HTTP upstream
  # traffic back to https forever and the deploy.<domain> UI never loads
  - kubectl patch cm/argocd-cmd-params-cm -n argocd --type=merge -p '{"data":{"server.insecure":"true"}}'
  - kubectl -n argocd rollout restart deployment argocd-server
%{ if root_app_git_url != "" ~}

  # 4. Apply ArgoCD Root App
  - kubectl apply -f /tmp/argocd-root-app.yaml
%{ endif ~}

  # 5. Install tailscaled (Phase 1: private overlay network). Install only —
  # joining the tailnet needs a preauth key that only exists after ArgoCD has
  # deployed Headscale, so that stays a deliberate admin-tools/setup-vpn.sh
  # step, not something automated here.
  - curl -fsSL https://tailscale.com/install.sh | sh
