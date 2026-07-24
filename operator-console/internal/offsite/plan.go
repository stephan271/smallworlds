package offsite

import (
	"context"
	"fmt"
)

// VersioningStatus is the object-versioning capability observed on a destination
// bucket. Only Enabled proves point-in-time protection; Unsupported and Unknown
// require an explicit operator acknowledgement.
type VersioningStatus string

const (
	VersioningEnabled     VersioningStatus = "enabled"
	VersioningDisabled    VersioningStatus = "disabled"
	VersioningUnsupported VersioningStatus = "unsupported"
	VersioningUnknown     VersioningStatus = "unknown"
)

// Inspection is the safe, read-only result of checking a destination bucket. It
// never carries credentials.
type Inspection struct {
	Reachable  bool             `json:"reachable"`
	Versioning VersioningStatus `json:"versioning"`
}

// RequiresAcknowledgement reports whether the operator must explicitly accept a
// versioning limitation because it could not be confirmed enabled. A destination
// with disabled versioning is a point-in-time risk; unsupported/unknown cannot
// be inspected — either way the console must not claim versioning is on.
func (inspection Inspection) RequiresAcknowledgement() bool {
	return inspection.Versioning != VersioningEnabled
}

// Inspector performs a safe, read-only check of a destination bucket, verifying
// access and object versioning without logging credentials. Production reads the
// real S3 API; tests inject a deterministic implementation.
type Inspector interface {
	Inspect(ctx context.Context, destination Destination, credentials Credentials) (Inspection, error)
}

// SecretEffect describes the Cluster Secret a plan will create or update: the
// secret's name and the keys it will hold — never the values.
type SecretEffect struct {
	SecretName string   `json:"secretName"`
	Keys       []string `json:"keys"`
}

// Implications are the stable, translatable reason codes a plan surfaces.
type Implications struct {
	Data       string `json:"data"`
	Cost       string `json:"cost"`
	Protection string `json:"protection"`
}

const (
	ImplicationData       = "offsite-copy-of-all-buckets-created"
	ImplicationCost       = "offsite-storage-and-egress-billed-by-destination"
	ImplicationProtection = "enables-offsite-disaster-protection"
)

// ChangePlan separates a Cluster Secret effect (credential values, out of Git)
// from the exact non-secret Git diff (destination shape), and states the data,
// cost, and protection implications.
type ChangePlan struct {
	Reference    Reference    `json:"reference"`
	GitDiff      string       `json:"gitDiff"`
	Secret       SecretEffect `json:"secret"`
	Implications Implications `json:"implications"`
}

// Plan builds the change plan for configuring an offsite destination. It fails
// if the destination is invalid, or if versioning could not be confirmed and the
// operator has not acknowledged the limitation.
func Plan(destination Destination, schedule, secretName, accessKeyID string, inspection Inspection, acknowledged bool) (ChangePlan, error) {
	if err := destination.Validate(); err != nil {
		return ChangePlan{}, err
	}
	if inspection.RequiresAcknowledgement() && !acknowledged {
		return ChangePlan{}, ErrVersioningUnacknowledged
	}
	reference := NewReference(destination, schedule, secretName, accessKeyID, acknowledged)
	return ChangePlan{
		Reference: reference,
		GitDiff:   gitDiff(reference),
		Secret:    SecretEffect{SecretName: reference.SecretName, Keys: []string{KeyAccessKeyID, KeySecretAccessKey}},
		Implications: Implications{
			Data:       ImplicationData,
			Cost:       ImplicationCost,
			Protection: ImplicationProtection,
		},
	}, nil
}

// gitDiff renders the non-secret Desired Configuration: a ConfigMap describing
// the destination shape and the *name* of the Cluster Secret the replicator
// mounts. It deliberately contains no access key or secret.
func gitDiff(reference Reference) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: backup-replicator-destination
  namespace: backup-system
data:
  endpoint: %q
  region: %q
  bucket: %q
  schedule: %q
  secretRef: %q
`,
		reference.Destination.Endpoint,
		reference.Destination.Region,
		reference.Destination.Bucket,
		reference.Schedule,
		reference.SecretName,
	)
}
