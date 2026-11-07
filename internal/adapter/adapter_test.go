package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDoesNotExposeBodies(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x.jsonl")
	data := `{"id":"1","sessionID":"s","taskID":"t","type":"usage","model":"m","tokens":{"input":2,"output":1},"content":"SECRET"}` + "\n"
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	a, _ := New("opencode")
	es, _, err := a.Parse(p)
	if err != nil || len(es) != 1 || es[0].Input == nil || *es[0].Input != 2 {
		t.Fatalf("%#v %v", es, err)
	}
}

func TestNestedModelWithTopLevelUsage(t *testing.T) {
	e, ok := normalize(map[string]any{
		"session_id": "root", "task_id": "task", "type": "assistant",
		"message": map[string]any{"model": "model"},
		"usage":   map[string]any{"input": float64(2)},
	}, "codex", "records.jsonl", 1)
	if !ok || e.Model != "model" || e.Input == nil || *e.Input != 2 {
		t.Fatalf("model lookup must be independent of usage location: %+v", e)
	}
	if e.Session != "root" || e.SourceSession != "root" {
		t.Fatalf("session-only records must preserve agent alias and source session: %+v", e)
	}
}

func TestParseWarnsForUnsupportedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	if err := os.WriteFile(path, []byte("{\"session_id\":\"root\",\"task_id\":\"task\",\"kind\":\"task_start\"}\n{\"unsupported\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, _ := New("codex")
	events, warnings, err := a.Parse(path)
	if err != nil || len(events) != 1 || len(warnings) != 1 {
		t.Fatalf("unsupported records must not disappear silently: events=%+v warnings=%v err=%v", events, warnings, err)
	}
}

func TestGeneratedIDsDistinguishNestedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "records.jsonl"), []byte(`{"session_id":"root","task_id":"task","kind":"usage","model":"model","usage":{"input":2}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := New("codex")
	events, warnings, err := a.Parse(dir)
	if err != nil || len(warnings) != 0 || len(events) != 2 {
		t.Fatalf("expected both source files: events=%+v warnings=%v err=%v", events, warnings, err)
	}
	if events[0].ID == events[1].ID {
		t.Fatalf("different files must not collide in usage deduplication: %q", events[0].ID)
	}
	for _, e := range events {
		if filepath.IsAbs(e.ID) {
			t.Fatalf("generated source IDs must not expose absolute paths: %q", e.ID)
		}
	}
}

func TestParsePreservesPrefixOfTruncatedJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	data := `{"id":"usage","session_id":"root","task_id":"task","kind":"usage","model":"model","usage":{"input":7}}` + "\n" + `{"id":"truncated"`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New("codex")
	if err != nil {
		t.Fatal(err)
	}
	events, warnings, err := a.Parse(path)
	if err != nil {
		t.Fatalf("a valid prefix must remain usable: %v", err)
	}
	if len(events) != 1 || events[0].ID != "usage" || events[0].Input == nil || *events[0].Input != 7 {
		t.Fatalf("valid prefix usage must be retained: %+v", events)
	}
	if len(warnings) != 1 {
		t.Fatalf("truncated suffix must be diagnosed: %+v", warnings)
	}
}
