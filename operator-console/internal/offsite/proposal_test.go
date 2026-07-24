package offsite

import (
	"strings"
	"testing"
)

func TestProposalFilesCarryTheReviewedDiffAndNoCredentials(t *testing.T) {
	credentials := Credentials{AccessKeyID: "003accesskeyid", SecretAccessKey: "K003topsecretvalue"}
	plan, err := Plan(
		Destination{Endpoint: "https://s3.eu-central-003.backblazeb2.com", Region: "eu-central-003", Bucket: "community-backups"},
		"", "", credentials.AccessKeyID,
		Inspection{Reachable: true, Versioning: VersioningEnabled},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	files := plan.ProposalFiles()
	content, ok := files[ProposalPath]
	if !ok {
		t.Fatalf("proposal is missing the destination file %q: %v", ProposalPath, files)
	}
	// What is proposed must be exactly what the operator reviewed.
	if content != plan.GitDiff {
		t.Fatalf("proposed file content diverges from the reviewed Git diff")
	}
	// The non-secret file references the Cluster Secret by name and describes the
	// destination shape, but must never carry a credential value.
	if !strings.Contains(content, "community-backups") || !strings.Contains(content, plan.Secret.SecretName) {
		t.Fatalf("proposed file lost the destination or secret reference: %s", content)
	}
	for _, secret := range []string{credentials.AccessKeyID, credentials.SecretAccessKey} {
		for path, body := range files {
			if strings.Contains(body, secret) {
				t.Fatalf("proposal file %q leaked a credential value", path)
			}
		}
	}
}

func TestSecretMaterialCarriesBothCredentialKeys(t *testing.T) {
	credentials := Credentials{AccessKeyID: "003accesskeyid", SecretAccessKey: "K003topsecretvalue"}
	material := SecretMaterial(credentials)
	if material[KeyAccessKeyID] != credentials.AccessKeyID {
		t.Fatalf("access key not written to the secret material")
	}
	if material[KeySecretAccessKey] != credentials.SecretAccessKey {
		t.Fatalf("secret access key not written to the secret material")
	}
	if len(material) != 2 {
		t.Fatalf("secret material holds unexpected keys: %v", material)
	}
}
