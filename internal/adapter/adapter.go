package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Event struct {
	ID, Session, Parent, Task, Kind, Provider, Model, Status string
	Time                                                     time.Time
	Input, CacheRead, CacheWrite, Output, Reasoning, Total   *int64
	Cumulative                                               bool
}

type Adapter interface {
	Name() string
	Parse(path string) ([]Event, []string, error)
}

func New(product string) (Adapter, error) {
	switch strings.ToLower(product) {
	case "claude", "claude-code":
		return native{name: "claude-code"}, nil
	case "codex":
		return native{name: "codex"}, nil
	case "opencode", "opencode-v2":
		return native{name: "opencode-v2"}, nil
	default:
		return nil, fmt.Errorf("unsupported product %q", product)
	}
}

type native struct{ name string }

func (n native) Name() string { return n.name }
func (n native) Parse(path string) ([]Event, []string, error) {
	var files []string
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && (strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".json")) {
				files = append(files, p)
			}
			return nil
		})
	} else {
		files = []string{path}
	}
	if err != nil {
		return nil, nil, err
	}
	var events []Event
	var warnings []string
	for _, f := range files {
		es, ws, e := parseFile(f, n.name)
		if e != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", f, e))
			continue
		}
		events = append(events, es...)
		warnings = append(warnings, ws...)
	}
	if len(events) == 0 && len(warnings) > 0 {
		return nil, warnings, fmt.Errorf("no supported records found")
	}
	return events, warnings, nil
}

func parseFile(path, product string) ([]Event, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var out []Event
	n := 0
	for {
		var v map[string]any
		if err = dec.Decode(&v); err == io.EOF {
			break
		}
		if err != nil {
			return out, nil, err
		}
		n++
		if e, ok := normalize(v, product, path, n); ok {
			out = append(out, e)
		}
	}
	return out, nil, nil
}

func normalize(v map[string]any, product, path string, line int) (Event, bool) {
	// Fixtures and exports may use a normalized envelope. Product-native aliases are
	// deliberately read without retaining message/tool bodies.
	e := Event{ID: str(v, "id", "uuid", "event_id"), Session: str(v, "session_id", "sessionID", "thread_id", "threadId", "agentId"), Parent: str(v, "parent_id", "parentID", "parent_thread_id"), Task: str(v, "task_id", "taskID", "turn_id", "turnId"), Kind: str(v, "kind", "type"), Provider: str(v, "provider"), Model: str(v, "model"), Status: str(v, "status")}
	if e.ID == "" {
		e.ID = fmt.Sprintf("%s:%d", filepath.Base(path), line)
	}
	if s := str(v, "timestamp", "created_at", "createdAt", "time"); s != "" {
		e.Time, _ = time.Parse(time.RFC3339Nano, s)
	}
	u := obj(v, "usage", "tokens", "token_usage")
	if len(u) == 0 {
		if m := obj(v, "message"); len(m) > 0 {
			e.Model = first(e.Model, str(m, "model"))
			u = obj(m, "usage", "tokens")
		}
	}
	e.Input = num(u, "input", "input_tokens", "inputTokens")
	e.CacheRead = num(u, "cache_read", "cache_read_input_tokens", "cacheRead")
	e.CacheWrite = num(u, "cache_write", "cache_creation_input_tokens", "cacheWrite")
	e.Output = num(u, "output", "output_tokens", "outputTokens")
	e.Reasoning = num(u, "reasoning", "reasoning_tokens")
	e.Total = num(u, "total", "total_tokens", "totalTokens")
	if b, ok := v["cumulative"].(bool); ok {
		e.Cumulative = b
	}
	if e.Session == "" {
		return Event{}, false
	}
	if e.Kind == "" && (e.Input != nil || e.Output != nil || e.Total != nil) {
		e.Kind = "usage"
	}
	return e, true
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func str(m map[string]any, ks ...string) string {
	for _, k := range ks {
		if v, ok := m[k].(string); ok {
			return v
		}
	}
	return ""
}
func obj(m map[string]any, ks ...string) map[string]any {
	for _, k := range ks {
		if v, ok := m[k].(map[string]any); ok {
			return v
		}
	}
	return nil
}
func num(m map[string]any, ks ...string) *int64 {
	for _, k := range ks {
		if f, ok := m[k].(float64); ok {
			x := int64(f)
			return &x
		}
	}
	return nil
}
