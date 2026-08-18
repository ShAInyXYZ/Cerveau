package session

import (
	"path/filepath"
	"testing"
)

// The WebUI must never redirect work already running from the terminal.
//
// The panel's folder picker changes the DEFAULT workspace for new sessions.
// A session created with an explicit workspace (what `crvcli -workspace` does)
// is stamped with it and must keep it, whatever the picker does afterwards.
func TestPickerDoesNotRedirectAnExplicitSession(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFSStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	cliWS := filepath.Join(dir, "cli-project")
	panelWS := filepath.Join(dir, "panel-project")

	s.SetWorkspace(panelWS) // panel points somewhere
	cli, cerr := s.CreateInWorkspace("from-terminal", cliWS)
	if cerr != nil {
		t.Fatal(cerr)
	}

	// user clicks the picker and chooses a different folder
	s.SetWorkspace(filepath.Join(dir, "somewhere-else"))

	got, gerr := s.Get(cli.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Workspace != cliWS {
		t.Errorf("CLI session workspace = %q, want %q — the panel picker\n"+
			"redirected a session started from the terminal", got.Workspace, cliWS)
	}
}

// A session created WITHOUT an explicit workspace legitimately follows the
// project default the picker sets — that is the per-project model, not a bug.
func TestSessionWithoutExplicitWorkspaceUsesTheDefault(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFSStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(dir, "project")
	s.SetWorkspace(proj)

	m, cerr := s.Create("from-panel")
	if cerr != nil {
		t.Fatal(cerr)
	}
	if m.Workspace != proj {
		t.Errorf("workspace = %q, want the project default %q", m.Workspace, proj)
	}
}
