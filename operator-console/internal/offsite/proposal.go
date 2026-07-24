package offsite

// This file carries the two halves of applying an approved offsite Change Plan,
// each kept on its own side of the trust split:
//
//   - ProposalFiles is the non-secret Desired Configuration committed to the
//     GitOps overlay through a proposal (branch/pull request). Its content is the
//     exact reviewed Git diff, so what is proposed equals what the operator saw.
//   - SecretMaterial is the credential values written to the Cluster Secret
//     through the authorized secret path — never to Git, an API response, or a log.

// Namespace is where the backup replicator, its destination ConfigMap, and the
// Cluster Secret it mounts all live. It matches the ConfigMap namespace rendered
// into the Git diff.
const Namespace = "backup-system"

// ProposalPath is the overlay-relative path of the destination ConfigMap the
// proposal adds or updates. It is deterministic so repeated proposals for the
// same profile touch the same file.
const ProposalPath = "protection/offsite-destination.yaml"

// ProposalTitle is the stable, secret-free title used when a provider (e.g.
// GitHub) needs a pull-request title.
const ProposalTitle = "Configure offsite backup destination"

// ProposalFiles is the set of overlay files a proposal writes: a single
// destination ConfigMap whose content is byte-for-byte the plan's reviewed Git
// diff. It deliberately carries no credential value — the ConfigMap only
// references the Cluster Secret by name.
func (plan ChangePlan) ProposalFiles() map[string]string {
	return map[string]string{ProposalPath: plan.GitDiff}
}

// SecretMaterial is the data map written to the Cluster Secret: the access key
// id and secret access key under their fixed keys. These values move only
// through the Launcher Vault and the authorized secret path; they never reach
// the ProposalFiles, a Reference, or an API response.
func SecretMaterial(credentials Credentials) map[string]string {
	return map[string]string{
		KeyAccessKeyID:     credentials.AccessKeyID,
		KeySecretAccessKey: credentials.SecretAccessKey,
	}
}
