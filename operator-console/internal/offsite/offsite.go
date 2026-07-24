// Package offsite models configuring and validating an offsite S3 destination
// for the cluster's backup replicator (tenants/backup-replicator). It enforces
// the repository's trust split: the destination *shape* — endpoint, region,
// bucket, schedule — is non-secret Desired Configuration that flows through the
// GitOps proposal path, while the access key and secret are credential values
// that flow only through the Launcher Vault and the Cluster Secret path and are
// never written to Git, returned to the browser, or logged.
//
// It also encodes the protection insight from doc/storage-and-backup.md §3: the
// offsite leg is the only real disaster protection, so it must be point-in-time
// capable. Where the destination's object versioning cannot be inspected, the
// operator must explicitly acknowledge the limitation rather than have the
// console claim versioning is enabled.
package offsite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// DefaultSecretName is the Cluster Secret the backup replicator mounts.
const DefaultSecretName = "replicator-config-secret"

// DefaultSchedule replicates nightly at 04:00, matching the replicator CronJob.
const DefaultSchedule = "0 4 * * *"

// The two credential keys stored in the Cluster Secret — names only ever appear
// as references; their values live in the Vault and the Secret.
const (
	KeyAccessKeyID     = "access_key_id"
	KeySecretAccessKey = "secret_access_key"
)

var (
	// ErrInvalidDestination is returned when a destination fails validation.
	ErrInvalidDestination = errors.New("offsite: invalid destination")
	// ErrMissingCredentials is returned when credentials are incomplete.
	ErrMissingCredentials = errors.New("offsite: missing credentials")
	// ErrVersioningUnacknowledged is returned when planning a destination whose
	// versioning cannot be confirmed without an explicit acknowledgement.
	ErrVersioningUnacknowledged = errors.New("offsite: unverifiable versioning requires acknowledgement")
)

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// Destination is the non-secret shape of an offsite S3 target.
type Destination struct {
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`
}

// Validate enforces an HTTPS endpoint (offsite traffic leaves the cluster), a
// region, and a syntactically valid bucket name.
func (destination Destination) Validate() error {
	parsed, err := url.Parse(strings.TrimSpace(destination.Endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%w: endpoint must be an https URL", ErrInvalidDestination)
	}
	if strings.TrimSpace(destination.Region) == "" {
		return fmt.Errorf("%w: region is required", ErrInvalidDestination)
	}
	if !bucketName.MatchString(destination.Bucket) {
		return fmt.Errorf("%w: bucket name", ErrInvalidDestination)
	}
	return nil
}

// Credentials are the offsite access key and secret. They are custodied in the
// Launcher Vault and the Cluster Secret; they are never part of a Reference,
// Git diff, or API response.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Valid reports whether both credential parts are present.
func (credentials Credentials) Valid() bool {
	return strings.TrimSpace(credentials.AccessKeyID) != "" && strings.TrimSpace(credentials.SecretAccessKey) != ""
}

// Reference is the secret-free record of a configured destination, safe to
// persist and to bind into a plan. It identifies the access key by a short
// fingerprint, never by value, and never carries the secret.
type Reference struct {
	Destination            Destination `json:"destination"`
	Schedule               string      `json:"schedule"`
	SecretName             string      `json:"secretName"`
	AccessKeyFingerprint   string      `json:"accessKeyFingerprint"`
	VersioningAcknowledged bool        `json:"versioningAcknowledged"`
	ConfigDigest           string      `json:"configDigest"`
}

// NewReference builds a secret-free reference for a destination and its access
// key id (used only to derive a non-reversible fingerprint for identification).
func NewReference(destination Destination, schedule, secretName, accessKeyID string, versioningAcknowledged bool) Reference {
	if strings.TrimSpace(schedule) == "" {
		schedule = DefaultSchedule
	}
	if strings.TrimSpace(secretName) == "" {
		secretName = DefaultSecretName
	}
	reference := Reference{
		Destination:            destination,
		Schedule:               schedule,
		SecretName:             secretName,
		AccessKeyFingerprint:   fingerprint(accessKeyID),
		VersioningAcknowledged: versioningAcknowledged,
	}
	reference.ConfigDigest = reference.digest()
	return reference
}

func (reference Reference) digest() string {
	canonical := strings.Join([]string{
		reference.Destination.Endpoint,
		reference.Destination.Region,
		reference.Destination.Bucket,
		reference.Schedule,
		reference.SecretName,
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// fingerprint returns a short, non-reversible identifier for an access key id,
// so the console can show which key is configured without storing its value.
func fingerprint(accessKeyID string) string {
	if strings.TrimSpace(accessKeyID) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(accessKeyID))
	return hex.EncodeToString(sum[:])[:12]
}
