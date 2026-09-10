package episodic

import (
	"sync"
	"testing"
)

func TestWriterEventsSerializesScopedSnapshots(t *testing.T) {
	w, err := Open(t.TempDir() + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	scoped := w.Scoped(map[string]any{"run_id": "r"})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := scoped.Append(Note, map[string]string{"kind": "snapshot-test"}); err != nil {
				t.Error(err)
			}
			if _, err := scoped.Events(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	events, err := w.Events()
	if err != nil || len(events) != 10 {
		t.Fatalf("%d events %v", len(events), err)
	}
	for i, event := range events {
		if event.ID != "evt_"+pad6(i+1) {
			t.Fatalf("torn sequence: %+v", events)
		}
	}
}
