package githttps

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

func TestNormalizeReferenceListAcceptsAnAuthenticatedEmptyRemote(t *testing.T) {
	references, err := normalizeReferenceList(nil, transport.ErrEmptyRemoteRepository)
	if err != nil {
		t.Fatalf("normalize empty remote: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("empty remote references = %#v", references)
	}

	wanted := []*plumbing.Reference{plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), plumbing.NewHash("1111111111111111111111111111111111111111"))}
	references, err = normalizeReferenceList(wanted, nil)
	if err != nil || len(references) != 1 || references[0] != wanted[0] {
		t.Fatalf("normal references = %#v, err = %v", references, err)
	}
}
