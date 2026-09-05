// Package planner contains the first-party Planner shipped with Cerveau.
// These are the canonical source files, not copies made during packaging.
package planner

import "embed"

// Files is read-only; installed RFX files cannot replace these build assets.
//
//go:embed pack.yaml ui/panel.html
var Files embed.FS
