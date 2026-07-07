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
  - path: /var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml
    permissions: '0644'
    content: |
      apiVersion: cert-manager.io/v1
      kind: ClusterIssuer
      metadata:
        name: letsencrypt-prod
      spec:
        selfSigned: {}

  - path: /etc/sysctl.d/99-kubernetes-cri.conf
    permissions: '0644'
    content: |
      fs.inotify.max_user_instances=8192
      fs.inotify.max_user_watches=524288

runcmd:
  # Create local directories (no persistent volume for staging)
  - mkdir -p /mnt/smallworlds-data/garage /mnt/smallworlds-data/immich-library /mnt/smallworlds-data/k3s
  
  - mkdir -p /var/lib/rancher
  - ln -sfn /mnt/smallworlds-data/k3s /var/lib/rancher/k3s

  # Get Dynamic IP and Install K3s
  - DYNAMIC_IP=$(hostname -I | awk '{print $1}')
  - sysctl --system
  - echo "ipv4" > ~/.curlrc
  # Explicit --node-name: at first boot from a snapshot the transient hostname
  # can still be the image builder's when k3s starts, registering a ghost node
%{ if golden_image ~}
  - INSTALL_K3S_SKIP_DOWNLOAD=true /usr/local/lib/k3s-install.sh server --cluster-init --node-ip=$DYNAMIC_IP --node-name=cc-staging-node-01 --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
%{ else ~}
  - curl -sfL https://get.k3s.io | sh -s - server --cluster-init --node-ip=$DYNAMIC_IP --node-name=cc-staging-node-01 --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
%{ endif ~}
  - export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
  - until kubectl get nodes | grep -v NotReady | grep -q Ready; do sleep 5; done

  # Export kubeconfig so Github Actions can easily scp it
  - cp /etc/rancher/k3s/k3s.yaml /root/k3s.yaml
  - sed -i "s/127.0.0.1/$DYNAMIC_IP/g" /root/k3s.yaml

  # Install ArgoCD
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
