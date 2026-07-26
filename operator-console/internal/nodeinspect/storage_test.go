package nodeinspect

import (
	"strings"
	"testing"
)

func TestInspectionCommandUsesOnlyAValidatedSelectedDataFilesystem(t *testing.T) {
	command, err := renderInspectionCommand("/data/smallworlds-acceptance")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "data_path='/data/smallworlds-acceptance'") || !strings.Contains(command, `disk_path="${disk_path%/*}"`) || !strings.Contains(command, `df -Pk "$disk_path"`) {
		t.Fatalf("inspection command does not resolve the selected filesystem:\n%s", command)
	}
	// The ownership check must look at the directory the operator chose, not a
	// fixed one. Judging a path that removal never touches leaves a blocker that
	// no amount of cleaning can clear.
	if strings.Contains(command, "/mnt/smallworlds-data") || !strings.Contains(command, `echo data="$(test "$data_path" != / && test -d "$data_path"`) {
		t.Fatalf("inspection command does not judge the selected data directory:\n%s", command)
	}
	for _, unsafe := range []string{"relative/path", "/data/../etc", "/data/value' ; id"} {
		if _, err := renderInspectionCommand(unsafe); err == nil {
			t.Fatalf("unsafe data directory %q accepted", unsafe)
		}
	}
}
