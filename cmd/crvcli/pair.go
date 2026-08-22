package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

// Pairing from a terminal.
//
// The invitation mechanism — short-lived one-shot code, slug, TTL — already
// existed, but only two things rendered it: the /pair HTML page and a JSON
// endpoint for the desktop panel's dialog. Both assume a browser on the
// machine running Cerveau.
//
// A headless install has neither. On a NAS or a server reached over SSH the
// pairing code was unreachable, so a phone could never be paired to it at all
// — the one deployment where remote access matters most.
//
// This is a client for the endpoint that already exists. It mints nothing and
// weakens nothing: /api/pair/invite still requires the loopback operator or an
// already-trusted device, and an SSH session on the host IS the loopback
// operator, which is the same proof of physical access the /pair page assumes.

type invite struct {
	Code      string `json:"code"`
	Gate      string `json:"gate"`
	Slug      string `json:"slug"`
	ExpiresIn int    `json:"expires_in"`
	QR        string `json:"qr"` // data: URI, unusable in a terminal
}

func (c *client) pair(showQR bool) error {
	raw, err := c.post("/api/pair/invite", nil)
	if err != nil {
		return err
	}
	// round-trip the decoded map into the struct rather than re-requesting
	buf, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var inv invite
	if err := json.Unmarshal(buf, &inv); err != nil {
		return fmt.Errorf("parse invite: %w", err)
	}
	if c.json {
		fmt.Println(string(buf))
		return nil
	}
	fmt.Print(renderInvite(inv, showQR))
	return nil
}

// renderInvite draws the invitation for a terminal.
//
// The QR is opt-in: even at the smallest usable size it is a forty-line block,
// and printing that on every invite would be hostile in an SSH session where
// the code alone is usually what is wanted.
func renderInvite(inv invite, showQR bool) string {
	var b strings.Builder

	if inv.ExpiresIn <= 0 {
		b.WriteString("\n  This invitation has EXPIRED — run `crv pair` again for a fresh one.\n\n")
		return b.String()
	}

	b.WriteString("\n  Pair a device with this machine\n")
	b.WriteString("  ───────────────────────────────\n\n")
	fmt.Fprintf(&b, "    code     %s\n", inv.Code)
	fmt.Fprintf(&b, "    address  %s\n", inv.Gate)
	if inv.Slug != "" {
		fmt.Fprintf(&b, "    link     %s/p/%s\n", inv.Gate, inv.Slug)
	}
	fmt.Fprintf(&b, "    expires  in %s\n", humanSeconds(inv.ExpiresIn))

	if showQR {
		if q, err := qrcode.New(inv.QRPayload(), qrcode.Low); err == nil {
			b.WriteString("\n")
			b.WriteString(indent(q.ToSmallString(false), "    "))
		}
	} else {
		b.WriteString("\n  Add --qr to print a scannable code.\n")
	}

	// State the properties that make this safe to read aloud over a call, and
	// the one that does not.
	b.WriteString("\n  One use, and only while it lasts. The device must already be on\n")
	b.WriteString("  your private network — this code does not grant network access.\n\n")
	return b.String()
}

// QRPayload is what the phone app scans: the same string the /pair page
// encodes, so one scanner handles both.
func (i invite) QRPayload() string {
	return i.Gate + "/p/" + i.Slug + "#" + i.Code
}

func humanSeconds(s int) string {
	if s < 60 {
		return fmt.Sprintf("%d sec", s)
	}
	m := s / 60
	if m == 1 {
		return "1 min"
	}
	return fmt.Sprintf("%d min", m)
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
