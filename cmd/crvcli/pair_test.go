package main

import (
	"strings"
	"testing"
)

// A headless install — a NAS, a server, a box reached only over SSH — has no
// browser to open the pairing page in, so the QR and code the WebUI shows are
// unreachable. The invitation itself is not browser-specific; only its
// presentation was.
func TestRenderInviteIsReadableInATerminal(t *testing.T) {
	out := renderInvite(invite{
		Code:      "K7M2QX",
		Gate:      "http://100.90.163.54:7701",
		Slug:      "a1b2c3",
		ExpiresIn: 300,
	}, false)

	if !strings.Contains(out, "K7M2QX") {
		t.Error("the code is the credential — it must be shown")
	}
	if !strings.Contains(out, "100.90.163.54:7701") {
		t.Error("the address is needed to reach the machine")
	}
	// A code with no stated lifetime invites someone to save it for later.
	if !strings.Contains(out, "5 min") && !strings.Contains(out, "300") {
		t.Errorf("expiry not stated:\n%s", out)
	}
}

// The QR is what makes a phone easy. In a terminal it has to be drawn with
// text, and only when asked — a 40-line block on every invite is hostile in
// an SSH session.
func TestQRIsOptOut(t *testing.T) {
	// ExpiresIn must be > 0: a live invite, since an expired one renders the
	// expiry notice and nothing else
	live := invite{Code: "AAA111", Gate: "http://x", Slug: "s", ExpiresIn: 300}
	plain := renderInvite(live, false)
	withQR := renderInvite(live, true)
	if len(withQR) <= len(plain) {
		t.Error("--qr should add the block, not replace the text")
	}
	if !strings.Contains(withQR, "AAA111") {
		t.Error("the code must still be readable alongside the QR")
	}
}

// An expired invitation must say so rather than print a code that will fail.
func TestExpiredInviteSaysSo(t *testing.T) {
	out := renderInvite(invite{Code: "OLD999", Gate: "http://x", Slug: "s", ExpiresIn: 0}, false)
	if !strings.Contains(strings.ToLower(out), "expired") {
		t.Errorf("an expired invite must be labelled:\n%s", out)
	}
}
