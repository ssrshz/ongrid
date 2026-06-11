package main

import (
	"slices"
	"testing"
)

// TestCoordinatorRosterHasCodeTools guards the regression where the read-code
// tools (HLD-012) were registered in the runtime toolbag but missing from the
// chat coordinator's curated whitelist — so the coordinator told users it had
// no way to read code. The coordinator can ONLY call tools in this list
// (filterToolsForAgent enforces it), so the code tools must be present here.
func TestCoordinatorRosterHasCodeTools(t *testing.T) {
	for _, want := range []string{"list_repo_sources", "read_source", "grep_source"} {
		if !slices.Contains(coordinatorToolNames, want) {
			t.Errorf("coordinator roster missing code tool %q (have %v)", want, coordinatorToolNames)
		}
	}
	// The lookup/triage baseline must also stay (don't accidentally drop it).
	for _, want := range []string{"query_knowledge", "query_devices", "list_database_sources", "analyze_database_status"} {
		if !slices.Contains(coordinatorToolNames, want) {
			t.Errorf("coordinator roster missing baseline tool %q", want)
		}
	}
}
