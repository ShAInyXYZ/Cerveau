package loop

import "testing"

func plan4() *Plan {
	return &Plan{Title: "Car", Steps: []PlanStep{
		{Title: "Scene", Files: []string{"index.html"}},
		{Title: "Car model", Files: []string{"car.js"}},
		{Title: "Physics", Files: []string{"car.js", "index.html"}},
		{Title: "Verify", Files: []string{"index.html"}},
	}}
}

func pass() Verdict { return Verdict{Pass: true, Check: "c"} }
func fail() Verdict { return Verdict{Pass: false, Check: "c"} }

func TestCursorAdvancesOnlyOnPass(t *testing.T) {
	s := NewSupervisor(plan4())
	if s.Next() != 0 {
		t.Fatal("should start at step 1")
	}
	d := s.Record(0, pass(), -1)
	if d.Action != "advance" || s.Next() != 1 {
		t.Fatalf("pass must advance: %+v next=%d", d, s.Next())
	}
	// A failure does NOT advance — the old design's whole problem.
	d = s.Record(1, fail(), -1)
	if d.Action != "retry" || s.Next() != 1 {
		t.Fatalf("fail must stay on the same step: %+v next=%d", d, s.Next())
	}
}

func TestImpossibleCheckCostsOneStepNotTheTask(t *testing.T) {
	s := NewSupervisor(plan4())
	s.Record(0, fail(), -1)
	d := s.Record(0, fail(), -1) // second failure of the same step
	if d.Action != "blocked" || !d.HandBack {
		t.Fatalf("a check that never passes must hand back: %+v", d)
	}
	if s.Blocked() != 0 {
		t.Fatal("step 1 should be the blocked one")
	}
	if s.Next() != -1 {
		t.Fatal("a blocked step stops the queue; later steps were planned on it")
	}
}

func TestRevisionReopensEarlierStepAndResumes(t *testing.T) {
	s := NewSupervisor(plan4())
	s.Record(0, pass(), -1)
	s.Record(1, pass(), -1)
	// step 3 discovers step 1 is incomplete
	d := s.Record(2, fail(), 0)
	if d.Action != "revise" || d.Step != 0 || d.Rev != 1 {
		t.Fatalf("want revise of step 1 as rev 1: %+v", d)
	}
	if s.Next() != 0 {
		t.Fatalf("cursor must go back to the revised step, got %d", s.Next())
	}
	// step 3 shares index.html with step 1, and step 4 has not passed yet,
	// so only genuinely-passed downstream steps are re-verified
	s.Record(0, pass(), -1)
	if s.Next() != 2 {
		t.Fatalf("after the revision passes, resume where we left off, got %d", s.Next())
	}
}

func TestRevisionCapStopsPingPong(t *testing.T) {
	s := NewSupervisor(plan4())
	s.Record(0, pass(), -1)
	s.Record(1, pass(), -1)
	// step 3 keeps sending step 1 back
	if d := s.Record(2, fail(), 0); d.Action != "revise" {
		t.Fatalf("first revision allowed: %+v", d)
	}
	s.Record(0, pass(), -1)
	// same pair again — rule 2 catches it before the cap does
	d := s.Record(2, fail(), 0)
	if d.Action != "blocked" || !d.HandBack {
		t.Fatalf("the same pair twice is a standoff, must hand back: %+v", d)
	}
}

func TestRevisionCapHardLimit(t *testing.T) {
	s := NewSupervisor(plan4())
	s.Record(0, pass(), -1)
	s.Record(1, pass(), -1)
	// different askers so rule 2 never fires; only the cap should stop it
	s.Record(2, fail(), 0) // rev 1
	s.Record(0, pass(), -1)
	s.Record(3, fail(), 0) // rev 2
	s.Record(0, pass(), -1)
	d := s.Record(2, fail(), 0) // would be rev 3
	if d.Action != "blocked" || !d.HandBack {
		t.Fatalf("cap of %d revisions must hand back: %+v", maxRevisions, d)
	}
}

func TestReverifyOnlyDownstreamSharingAFile(t *testing.T) {
	s := NewSupervisor(plan4())
	s.Record(0, pass(), -1) // index.html
	s.Record(1, pass(), -1) // car.js
	s.Record(2, pass(), -1) // car.js + index.html
	// step 4 says step 2 (car.js) is incomplete
	d := s.Record(3, fail(), 1)
	if d.Action != "revise" {
		t.Fatalf("want revise: %+v", d)
	}
	// step 3 shares car.js and had passed → must be re-checked.
	// step 1 is EARLIER, so it is not downstream and is untouched.
	if len(d.Reverify) != 1 || d.Reverify[0] != 2 {
		t.Fatalf("want only step 3 re-verified, got %v", d.Reverify)
	}
}

func TestDoneOnlyWhenEveryStepPassed(t *testing.T) {
	s := NewSupervisor(plan4())
	for i := 0; i < 3; i++ {
		if d := s.Record(i, pass(), -1); d.Action == "done" {
			t.Fatalf("done too early at step %d", i+1)
		}
	}
	if d := s.Record(3, pass(), -1); d.Action != "done" {
		t.Fatalf("want done: %+v", d)
	}
	if !s.Done() {
		t.Fatal("Done() should agree")
	}
}
