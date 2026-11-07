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
