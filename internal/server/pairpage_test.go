package server

import (
	"strings"
	"testing"
	"time"
)

// A pairing session is a short-lived, one-shot invitation: a 4-char URL slug
// plus the 6-char code the user types (or the QR carries). It must expire, it
// must not be reusable, and its code must never be guessable from the slug.
func TestPairSessionLifecycle(t *testing.T) {
	s := newPairSessions()
	inv := s.mint("https://gate.example:7443")

	if len(inv.Slug) != 4 {
		t.Errorf("slug should be 4 chars for easy typing, got %q", inv.Slug)
	}
	if len(inv.Code) != 6 {
		t.Errorf("code should be 6 chars, got %q", inv.Code)
	}
	if strings.Contains(inv.Code, inv.Slug) || strings.Contains(inv.Slug, inv.Code) {
		t.Error("the slug must not reveal the code — the URL is the weaker secret")
	}

	// the slug resolves while fresh
	got, ok := s.bySlug(inv.Slug)
	if !ok || got.Code != inv.Code {
		t.Fatal("a fresh slug must resolve to its invitation")
	}

	// the CODE is what authorizes pairing, and it is one-shot
	if _, ok := s.consume(inv.Code); !ok {
		t.Fatal("a fresh code must authorize once")
	}
	if _, ok := s.consume(inv.Code); ok {
		t.Error("a consumed code must never authorize again (replay)")
	}
}

func TestPairSessionExpires(t *testing.T) {
	s := newPairSessions()
	inv := s.mint("https://gate.example:7443")
	// age it past the TTL
	s.mu.Lock()
	s.items[inv.Code].expires = time.Now().Add(-time.Second)
	s.mu.Unlock()

	if _, ok := s.consume(inv.Code); ok {
		t.Error("an expired code must not authorize")
	}
	if _, ok := s.bySlug(inv.Slug); ok {
		t.Error("an expired slug must not resolve")
	}
}

// Codes and slugs must avoid characters a human misreads on a screen.
func TestPairAlphabetIsUnambiguous(t *testing.T) {
	s := newPairSessions()
	for i := 0; i < 50; i++ {
		inv := s.mint("https://gate.example:7443")
		for _, r := range inv.Code + inv.Slug {
			if strings.ContainsRune("O0I1L", r) {
				t.Fatalf("ambiguous character %q in %q/%q", r, inv.Code, inv.Slug)
			}
		}
	}
}

// The page must carry the gate origin so the phone learns WHERE to pair
// without the address being compiled into the app.
func TestInvitationCarriesGate(t *testing.T) {
	s := newPairSessions()
	inv := s.mint("https://gate.example:7443")
	if inv.Gate != "https://gate.example:7443" {
		t.Errorf("invitation must carry the gate origin, got %q", inv.Gate)
	}
	payload := inv.QRPayload()
	if !strings.Contains(payload, inv.Gate) || !strings.Contains(payload, inv.Code) {
		t.Errorf("QR payload must carry gate AND code: %q", payload)
	}
}

// Opening the pair dialog repeatedly must NOT mint a new invitation each
// time: several live codes at once is strictly worse security, and the user
// expects the countdown to keep running when they reopen it.
func TestPairSessionReusesLiveInvitation(t *testing.T) {
	s := newPairSessions()
	first := s.current("https://gate.example:7443")
	second := s.current("https://gate.example:7443")
	if first.Code != second.Code {
		t.Errorf("reopening should reuse the live invitation: %q vs %q", first.Code, second.Code)
	}

	// once it expires, the next open mints a fresh one
	s.mu.Lock()
	s.items[first.Code].expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	third := s.current("https://gate.example:7443")
	if third.Code == first.Code {
		t.Error("an expired invitation must be replaced, not reused")
	}
}

// A consumed invitation must not be handed out again either.
func TestPairSessionAfterUseMintsFresh(t *testing.T) {
	s := newPairSessions()
	first := s.current("https://gate.example:7443")
	if _, ok := s.consume(first.Code); !ok {
		t.Fatal("fresh code should consume")
	}
	next := s.current("https://gate.example:7443")
	if next.Code == first.Code {
		t.Error("a used invitation must not be reissued")
	}
}
