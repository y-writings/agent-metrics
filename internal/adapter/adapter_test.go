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
