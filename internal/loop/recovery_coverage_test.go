package loop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func coverageReceipt(path, source string, start, end int) string {
	from := 1 + strings.Count(source[:start], "\n")
	to := from
	if end > start {
		to = 1 + strings.Count(source[:end-1], "\n")
	}
	meta, _ := json.Marshal(map[string]any{"path": path, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(source))), "start_offset": start, "end_offset": end, "total_bytes": len(source), "total_lines": 1 + strings.Count(source, "\n"), "from_line": from, "to_line": to, "partial_start": start > 0 && source[start-1] != '\n', "partial_end": end < len(source) && end > start && source[end-1] != '\n', "eof": end == len(source)})
	var body strings.Builder
	for i, line := range strings.Split(strings.TrimSuffix(source[start:end], "\n"), "\n") {
		fmt.Fprintf(&body, "%d\t%s\n", from+i, line)
	}
	return "[read " + string(meta) + "]\n" + body.String()
}

func writeCoverageFile(t *testing.T, ws, path, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, path), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryCoverageCountsUnionNotCalls(t *testing.T) {
	ws := t.TempDir()
	source := strings.Repeat("x", 4000)
	writeCoverageFile(t, ws, "a.js", source)
	c := newRecoveryCoverage(ws)
	c.observeRead(coverageReceipt("a.js", source, 0, 1023), false)
	if got := c.freshBytes(); got != 1023 {
		t.Fatal(got)
	}
	c.observeRead(coverageReceipt("a.js", source, 100, 1024), false)
	if got := c.freshBytes(); got != 1024 {
		t.Fatal(got)
	}
	c.observeRead(coverageReceipt("a.js", source, 100, 1024), false)
	c.observeRead(coverageReceipt("./a.js", source, 0, 1000), false)
	if got := c.freshBytes(); got != 1024 {
		t.Fatal("overlap/alias counted twice", got)
	}
	c.observeRead(coverageReceipt("a.js", source, 1024, 2000), false)
	if got := c.freshBytes(); got != 2000 {
		t.Fatal(got)
	}
}

func TestRecoveryCoverageSeededEvidenceCannotBuyExtensionAgain(t *testing.T) {
	ws := t.TempDir()
	source := strings.Repeat("x", 5000)
	writeCoverageFile(t, ws, "a.js", source)
	c := newRecoveryCoverage(ws)
	// More observations than retained recovery excerpts; all intervals count.
	for i := 0; i < 40; i++ {
		c.observeRead(coverageReceipt("a.js", source, i*100, i*100+100), true)
	}
	c.observeRead(coverageReceipt("a.js", source, 0, 4500), false)
	if got := c.freshBytes(); got != 500 {
		t.Fatalf("historical coverage forgotten: %d", got)
	}
	// Seeding is conservative even if called after a fresh observation.
	c.observeRead(coverageReceipt("a.js", source, 4000, 4500), true)
	if got := c.freshBytes(); got != 0 {
		t.Fatal(got)
	}
}

func TestRecoveryCoverageHardlinkAliasesAndIndependentFiles(t *testing.T) {
	ws := t.TempDir()
	source := strings.Repeat("x", 2000)
	writeCoverageFile(t, ws, "a.js", source)
	if err := os.Link(filepath.Join(ws, "a.js"), filepath.Join(ws, "alias.js")); err != nil {
		t.Fatal(err)
	}
	writeCoverageFile(t, ws, "independent.js", source)
	c := newRecoveryCoverage(ws)
	c.observeRead(coverageReceipt("a.js", source, 0, 1200), false)
	c.observeRead(coverageReceipt("alias.js", source, 0, 1200), false)
	if got := c.freshBytes(); got != 1200 {
		t.Fatal("hardlink counted twice", got)
	}
	c.observeRead(coverageReceipt("independent.js", source, 0, 1200), false)
	if got := c.freshBytes(); got != 2400 {
		t.Fatal("independent file omitted", got)
	}
}

func TestRecoveryCoverageRevalidatesCurrentVersionAtGrant(t *testing.T) {
	ws := t.TempDir()
	a, b := strings.Repeat("a", 2000), strings.Repeat("b", 2000)
	writeCoverageFile(t, ws, "a.js", a)
	c := newRecoveryCoverage(ws)
	c.observeRead(coverageReceipt("a.js", a, 0, 2000), true)
	c.observeRead(coverageReceipt("a.js", a, 0, 2000), false)
	if got := c.freshBytes(); got != 0 {
		t.Fatal(got)
	}
	writeCoverageFile(t, ws, "a.js", b)
	c.observeRead(coverageReceipt("a.js", a, 0, 2000), false)
	if got := c.freshBytes(); got != 0 {
		t.Fatal("stale receipt credited", got)
	}
	c.observeRead(coverageReceipt("a.js", b, 0, 1500), false)
	if got := c.freshBytes(); got != 1500 {
		t.Fatal(got)
	}
	writeCoverageFile(t, ws, "a.js", a)
	if got := c.freshBytes(); got != 0 {
		t.Fatal("obsolete version or replay credited", got)
	}
	c.observeRead(coverageReceipt("a.js", a, 0, 2000), false)
	if got := c.freshBytes(); got != 0 {
		t.Fatal("reverted baseline reacquired", got)
	}
}

func TestRecoveryCoverageRejectsUnprovenOrOutsideSource(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	source := strings.Repeat("é", 1000)
	writeCoverageFile(t, ws, "a.js", source)
	writeCoverageFile(t, outside, "secret.js", source)
	if err := os.Symlink(filepath.Join(outside, "secret.js"), filepath.Join(ws, "outside.js")); err != nil {
		t.Fatal(err)
	}
	good := coverageReceipt("a.js", source, 0, 1200)
	for name, out := range map[string]string{
		"legacy":           "1\t" + source,
		"wrong body":       strings.Replace(good, "1\té", "1\tx", 1),
		"wrong lines":      strings.Replace(good, "1\té", "2\té", 1),
		"wrong size":       strings.Replace(good, `"total_bytes":2000`, `"total_bytes":2001`, 1),
		"wrong end":        strings.Replace(good, `"end_offset":1200`, `"end_offset":999999999`, 1),
		"negative start":   strings.Replace(good, `"start_offset":0`, `"start_offset":-1`, 1),
		"partial utf8":     coverageReceipt("a.js", source, 1, 1200),
		"outside absolute": coverageReceipt(filepath.Join(outside, "secret.js"), source, 0, 1200),
		"outside parent":   coverageReceipt("../secret.js", source, 0, 1200),
		"outside symlink":  coverageReceipt("outside.js", source, 0, 1200),
	} {
		t.Run(name, func(t *testing.T) {
			c := newRecoveryCoverage(ws)
			c.observeRead(out, false)
			if got := c.freshBytes(); got != 0 {
				t.Fatal("invalid read credited", got)
			}
		})
	}
}

func TestRecoveryCoverageBoundsFileVersionTracksAndSize(t *testing.T) {
	ws := t.TempDir()
	source := strings.Repeat("x", 1200)
	c := newRecoveryCoverage(ws)
	for i := 0; i < 70; i++ {
		path := fmt.Sprintf("%d.js", i)
		writeCoverageFile(t, ws, path, source)
		c.observeRead(coverageReceipt(path, source, 0, 1200), false)
	}
	if got := c.freshBytes(); got != 64*1200 {
		t.Fatal("unbounded or missing track cap", got)
	}
	oversized := strings.Repeat("x", recoveryFileLimit+1)
	writeCoverageFile(t, ws, "large.js", oversized)
	separate := newRecoveryCoverage(ws)
	separate.observeRead(coverageReceipt("large.js", oversized, 0, 1200), false)
	if got := separate.freshBytes(); got != 0 {
		t.Fatal("oversized source credited", got)
	}
}
