# SmallWorlds Restore Procedures

This document outlines the standard operating procedures for restoring data in the event of partial or catastrophic data loss in the SmallWorlds cluster.

## 1. PostgreSQL Databases (CloudNativePG)

CNPG automatically backs up the database to Garage S3 via Barman. To restore a database, you must bootstrap a new cluster from the backup of the old one.

1. **Identify the source cluster and backup:**
   ```bash
   kubectl get backups -n <namespace>
   ```
2. **Create a new cluster with the `recovery` bootstrap method:**
   Example manifest for restoring to a new cluster named `my-db-restore`:
   ```yaml
   apiVersion: postgresql.cnpg.io/v1
   kind: Cluster
   metadata:
     name: my-db-restore
     namespace: <namespace>
   spec:
     instances: 2
     bootstrap:
       recovery:
         source: <original-cluster-name>
     externalClusters:
       - name: <original-cluster-name>
         barmanObjectStore:
           destinationPath: s3://postgres-backups/
           endpointURL: http://garage.garage-system.svc.cluster.local:3900
           s3Credentials:
             accessKeyId:
               name: garage-auth-secret
               key: access-key
             secretAccessKey:
               name: garage-auth-secret
               key: secret-key
   ```
3. **Apply the manifest** and wait for the new cluster to become ready. Once verified, update the application to point to the new cluster.

## 2. Cluster State and Workloads (Velero)

Velero backs up Kubernetes resources (Deployments, Services, ConfigMaps, etc.).

1. **List available backups:**
   ```bash
   velero backup get
   ```
2. **Restore a specific namespace:**
   ```bash
   velero restore create --from-backup <backup-name> --include-namespaces <namespace>
   ```
3. **Restore specific resources:**
   ```bash
   velero restore create --from-backup <backup-name> --include-resources deployment,service --include-namespaces <namespace>
   ```
4. **Check restore status:**
   ```bash
   velero restore get
   velero restore describe <restore-name>
   ```

## 3. Application Data (S3 / Garage)

If data is lost from the primary Garage S3 instance, you can sync it back from the off-site backup (Home Garage) using `rclone`.

1. **Start a temporary rclone pod with shell access:**
   ```bash
   kubectl run rclone-restore -it --rm --image=rclone/rclone:1.64 --restart=Never -- /bin/sh
   ```
2. **Configure rclone (or mount the secret):**
   ```bash
   rclone config
   # Add your cloud Garage as 'dest'
   # Add your home Garage as 'source'
   ```
3. **Perform the restore sync:**
   ```bash
   rclone sync source:<bucket-name> dest:<bucket-name> -v --dry-run
   ```
4. **Remove `--dry-run`** to actually execute the sync once you have verified it will do the right thing.

## 4. Disaster Recovery (Complete Cluster Rebuild)

If the entire cluster is lost:
1. Re-run `smallworlds-init.sh` to provision the base VMs, K3s, and GitOps controller.
2. ArgoCD will sync the base infrastructure (Garage, Traefik, Keycloak, etc.).
3. Wait for Garage to be online.
4. Execute **Procedure 3 (S3 Data)** to restore S3 buckets from the home server.
5. Execute **Procedure 1 (PostgreSQL)** to recover databases from the newly restored Garage buckets.
6. Let ArgoCD finish syncing the application deployments, which will now reconnect to their restored databases and data.
