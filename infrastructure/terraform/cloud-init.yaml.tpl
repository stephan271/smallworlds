#cloud-config
package_update: true
packages:
  - curl
  - jq

write_files:
  - path: /var/lib/rancher/k3s/server/manifests/smallworlds-secrets.yaml
    permissions: '0600'
    content: |
      ---
      apiVersion: v1
      kind: Namespace
      metadata:
        name: keycloak
      ---
      apiVersion: v1
      kind: Namespace
      metadata:
        name: nextcloud
      ---
      apiVersion: v1
      kind: Namespace
      metadata:
        name: immich
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: keycloak-admin-creds
        namespace: keycloak
      type: Opaque
      stringData:
        admin-password: "${keycloak_admin_password}"
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: nextcloud-admin-creds
        namespace: nextcloud
      type: Opaque
      stringData:
        nextcloud-username: "admin"
        nextcloud-password: "${nextcloud_admin_password}"
        admin-email: "${admin_email}"
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: immich-admin-creds
        namespace: immich
      type: Opaque
      stringData:
        email: "${admin_email}"
        password: "${immich_admin_password}"
        name: "Admin"
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: repo-git-creds
        namespace: argocd
        labels:
          argocd.argoproj.io/secret-type: repository
      type: Opaque
      stringData:
        url: "${git_url}"
        username: "${git_username}"
        password: "${git_password}"
      ---
      apiVersion: v1
      kind: Namespace
      metadata:
        name: stalwart
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: stalwart-dns-secrets
        namespace: stalwart
      type: Opaque
      stringData:
        hcloud-token: "${hcloud_token}"
        domain: "${domain_name}"
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: stalwart-admin-secret
        namespace: stalwart
      type: Opaque
      stringData:
        password: "${stalwart_admin_password}"
        api-key: "${stalwart_admin_password}"
        recovery-admin: "admin:${stalwart_admin_password}"
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: keycloak-stalwart-secret
        namespace: keycloak
      type: Opaque
      stringData:
        password: "${stalwart_admin_password}"
      ---
      apiVersion: v1
      kind: Namespace
      metadata:
        name: forgejo
      ---
      apiVersion: v1
      kind: Secret
      metadata:
        name: forgejo-admin-creds
        namespace: forgejo
      type: Opaque
      stringData:
        username: "gitadmin"
        password: "${forgejo_admin_password}"
        email: "${admin_email}"

  - path: /var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml
    permissions: '0644'
    content: |
      apiVersion: cert-manager.io/v1
      kind: ClusterIssuer
      metadata:
        name: letsencrypt-prod
      spec:
        acme:
          server: https://acme-v02.api.letsencrypt.org/directory
          email: "${admin_email}"
          privateKeySecretRef:
            name: letsencrypt-prod
          solvers:
          - http01:
              ingress:
                class: traefik

  - path: /var/lib/rancher/k3s/server/manifests/coredns-custom.yaml
    permissions: '0644'
    content: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: coredns-custom
        namespace: kube-system
      data:
        smallworlds.server: |
          ${domain_name}:53 {
            hosts {
              ${server_ip} identity.${domain_name} files.${domain_name} photos.${domain_name} git.${domain_name} mail.${domain_name}
              fallthrough
            }
            forward . /etc/resolv.conf
          }

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
          repoURL: '${git_url}'
          targetRevision: HEAD
          path: infrastructure/kubernetes/apps
        destination:
          server: 'https://kubernetes.default.svc'
          namespace: argocd
        syncPolicy:
          automated:
            prune: true
            selfHeal: true
          syncOptions:
            - CreateNamespace=true
            - SkipDryRunOnMissingResource=true

runcmd:
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

  # Relocate K3s data to the persistent volume to survive VM deletion
  - if [ -d "/var/lib/rancher/k3s" ] && [ ! -L "/var/lib/rancher/k3s" ]; then cp -a /var/lib/rancher/k3s/* /mnt/smallworlds-data/k3s/ ; rm -rf /var/lib/rancher/k3s ; fi
  - mkdir -p /var/lib/rancher
  - ln -sfn /mnt/smallworlds-data/k3s /var/lib/rancher/k3s

  # 2. Install K3s (This will automatically apply the manifests in /var/lib/rancher/k3s/server/manifests/)
  - echo "ipv4" > ~/.curlrc
  - curl -sfL https://get.k3s.io | sh -s - server --cluster-init --disable traefik
  - export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
  - until kubectl get nodes | grep -v NotReady | grep -q Ready; do sleep 5; done

  # 3. Install ArgoCD
  - kubectl create namespace argocd || true
  - kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml --server-side --force-conflicts
  - kubectl patch cm/argocd-cm -n argocd --type=merge -p '{"data":{"kustomize.buildOptions":"--enable-helm"}}'

  # 4. Apply ArgoCD Root App
  - kubectl apply -f /tmp/argocd-root-app.yaml
