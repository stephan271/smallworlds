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
	if !strings.Contains(command, "disk_path='/data/smallworlds-acceptance'") || !strings.Contains(command, `disk_path="${disk_path%/*}"`) || !strings.Contains(command, `df -Pk "$disk_path"`) {
		t.Fatalf("inspection command does not resolve the selected filesystem:\n%s", command)
	}
	for _, unsafe := range []string{"relative/path", "/data/../etc", "/data/value' ; id"} {
		if _, err := renderInspectionCommand(unsafe); err == nil {
			t.Fatalf("unsafe data directory %q accepted", unsafe)
		}
	}
}
