package episodic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type EventType string

const (
	MsgUser      EventType = "msg.user"
	MsgAssistant EventType = "msg.assistant"
	ToolCall     EventType = "tool.call"
	ToolResult   EventType = "tool.result"
	Note         EventType = "note"
	Plan         EventType = "plan"
	TurnClose    EventType = "turn.close"
	Checkpoint   EventType = "checkpoint"
	Err          EventType = "error"
	Interrupt    EventType = "interrupt"
	Aborted      EventType = "aborted"
)

type Event struct {
	ID      string          `json:"id"`
	TS      time.Time       `json:"ts"`
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type Writer struct {
	mu   sync.Mutex
	f    *os.File
	path string
	seq  int
}

func Open(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	seq, err := recoverFile(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, path: path, seq: seq}, nil
}

func (w *Writer) Append(t EventType, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal payload: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	ev := Event{
		ID:      "evt_" + pad6(w.seq),
		TS:      time.Now().UTC(),
		Type:    t,
		Payload: raw,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		w.seq--
		return Event{}, fmt.Errorf("marshal event: %w", err)
	}
	line = append(line, '\n')
	if _, err := w.f.Write(line); err != nil {
		w.seq--
		return Event{}, fmt.Errorf("append: %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return Event{}, fmt.Errorf("fsync: %w", err)
	}
	return ev, nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

func Replay(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	events, _, err := readValid(f)
	return events, err
}

type State struct {
	Messages       []Event           `json:"messages"`
	Plan           json.RawMessage   `json:"plan,omitempty"`
	LastCheckpoint *Event            `json:"last_checkpoint,omitempty"`
	Counts         map[EventType]int `json:"counts"`
}

func Fold(events []Event) *State {
	st := &State{Counts: map[EventType]int{}}
	for _, ev := range events {
		st.Counts[ev.Type]++
		switch ev.Type {
		case MsgUser, MsgAssistant:
			st.Messages = append(st.Messages, ev)
		case Plan:
			st.Plan = ev.Payload
		case Checkpoint:
			cp := ev
			st.LastCheckpoint = &cp
		}
	}
	return st
}

func recoverFile(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if info.Size() == 0 {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	events, validEnd, err := readValid(f)
	f.Close()
	if err != nil {
		return 0, err
	}
	if validEnd < info.Size() {
		if err := os.Truncate(path, validEnd); err != nil {
			return 0, fmt.Errorf("truncate partial tail: %w", err)
		}
	}
	if validEnd > 0 {
		if err := ensureTrailingNewline(path); err != nil {
			return 0, err
		}
	}
	if len(events) == 0 {
		return 0, nil
	}
	return seqOf(events[len(events)-1].ID), nil
}

func ensureTrailingNewline(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = f.WriteAt([]byte{'\n'}, info.Size())
	return err
}

func readValid(r io.Reader) ([]Event, int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var events []Event
	var off int64
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(string(line))
			if trimmed == "" {
				off += int64(len(line))
			} else {
				var ev Event
				if jerr := json.Unmarshal([]byte(trimmed), &ev); jerr != nil || ev.ID == "" {
					break
				}
				events = append(events, ev)
				off += int64(len(line))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return events, off, readErr
		}
	}
	return events, off, nil
}

func seqOf(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "evt_"))
	return n
}

func pad6(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}
