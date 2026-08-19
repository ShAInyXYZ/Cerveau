package loop

import "strings"

import "testing"

// A model that pipes a failing command through od/xxd/hexdump loses the very
// error it is trying to read, then repeats the call hoping for a different
// answer. This really happened: 8 identical `node -e ... | od -c | sed -n 1,1p`
// calls in a row ended the chess benchmark on guard_loop_detected.
//
// The generic "change the arguments" hint cannot fix that, because the model
// cannot see what it destroyed. The hint has to name the pipe.
func TestRepeatHintNamesTheManglingPipe(t *testing.T) {
	cmd := `node -e "for (const [x,y] of a[0]) {}" 2>&1 | od -c | sed -n '1,1p'`
	got := repeatHintFor("bash", "0000000   [   e   v   a   l   ]   :   3", cmd)

	if !strings.Contains(got, "od") {
		t.Errorf("hint never mentions the od pipe that hid the error.\ngot: %s", got)
	}
	if !strings.Contains(strings.ToLower(got), "without") {
		t.Errorf("hint does not tell the model to re-run without the pipe.\ngot: %s", got)
	}
}

// Truncating with head/sed on a command that FAILED hides the error too.
func TestRepeatHintCatchesTruncationOfErrors(t *testing.T) {
	cmd := `python3 build.py 2>&1 | head -c 40`
	got := repeatHintFor("bash", "Traceback (most recent cal", cmd)
	if !strings.Contains(strings.ToLower(got), "full") {
		t.Errorf("hint should ask for the full output.\ngot: %s", got)
	}
}

// An ordinary repeated call with no mangling pipe keeps the generic advice.
func TestRepeatHintFallsBackWhenNoPipe(t *testing.T) {
	got := repeatHintFor("bash", "up to date", "npm install")
	if !strings.Contains(got, "repeating it will not help") {
		t.Errorf("expected the generic hint.\ngot: %s", got)
	}
}

// The literal command from the chess run that looped 8 times.
func TestRealWorldOdLoopGetsNamedHint(t *testing.T) {
	cmd := `cd /home/shiny/Pictures/Benchmark && node -e "
const a = [[1,2],[3,4]];
for (const [x, y] of a[0]) console.log(x, y);
" 2>&1 | od -c | sed -n '1,1p'`
	out := `0000000   [   e   v   a   l   ]   :   3  \n   f   o   r       (   c   o`
	got := repeatHintFor("bash", out, cmd)
	t.Logf("HINT: %s", got)
	if got == genericRepeatHint("bash") {
		t.Fatal("real-world od loop still gets the useless generic hint")
	}
}
