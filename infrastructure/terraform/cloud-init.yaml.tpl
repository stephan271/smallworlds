#cloud-config
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
              ${server_ip} identity.${domain_name} files.${domain_name} photos.${domain_name} git.${domain_name} mail.${domain_name} meet.${domain_name}
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
  - sysctl --system
  - echo "ipv4" > ~/.curlrc
  # Explicit --node-name: at first boot from a snapshot the transient hostname
  # can still be the image builder's when k3s starts, registering a ghost node
%{ if golden_image ~}
  # Golden image: k3s binary and container images are baked in — just
  # regenerate the systemd unit with node-specific args and start
  - INSTALL_K3S_SKIP_DOWNLOAD=true /usr/local/lib/k3s-install.sh server --cluster-init --node-ip=${server_ip} --node-name=cc-pilot-node-01 --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
%{ else ~}
  - curl -sfL https://get.k3s.io | sh -s - server --cluster-init --node-ip=${server_ip} --node-name=cc-pilot-node-01 --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
%{ endif ~}
  - export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
  - until kubectl get nodes | grep -v NotReady | grep -q Ready; do sleep 5; done

  # Automatically restore Let's Encrypt certificates if a backup exists
  - if [ -f "/mnt/smallworlds-data/certs-backup.yaml" ]; then kubectl apply -f /mnt/smallworlds-data/certs-backup.yaml; fi

  # 3. Install ArgoCD
  - kubectl create namespace argocd || true
  - kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml --server-side --force-conflicts
  - |
    cat << 'EOF' > /tmp/argocd-cm-patch.yaml
    data:
      kustomize.buildOptions: "--enable-helm"
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

  # 4. Apply ArgoCD Root App
  - kubectl apply -f /tmp/argocd-root-app.yaml
