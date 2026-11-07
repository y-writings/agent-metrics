package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/y-writings/agent-metrics/internal/adapter"
	"github.com/y-writings/agent-metrics/internal/report"
	"os"
)

func main() {
	product := flag.String("product", "", "claude-code, codex, or opencode-v2")
	path := flag.String("records", "", "native record file or directory")
	parent := flag.String("parent", "", "parent session/thread ID")
	task := flag.String("task", "", "task/turn ID")
	flag.Parse()
	if *product == "" || *path == "" || *parent == "" || *task == "" {
		fmt.Fprintln(os.Stderr, "product, records, parent, and task are required")
		flag.Usage()
		os.Exit(2)
	}
	a, err := adapter.New(*product)
	if err != nil {
		fatal(err)
	}
	events, warnings, err := a.Parse(*path)
	if err != nil {
		fatal(err)
	}
	r := report.Build(a.Name(), *parent, *task, events, warnings)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "agent-metrics:", err); os.Exit(1) }
