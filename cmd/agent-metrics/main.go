package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/y-writings/agent-metrics/internal/adapter"
	"github.com/y-writings/agent-metrics/internal/report"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "collect" {
		fmt.Fprintln(os.Stderr, "usage: agent-metrics collect --product PRODUCT --agent AGENT_ID [--execution EXECUTION_ID] [--records PATH]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	product := fs.String("product", "", "claude-code, codex, or opencode-v2")
	agent := fs.String("agent", "", "agent/session identifier returned when the sub-agent was created")
	execution := fs.String("execution", "", "one execution identifier (required when an agent was reused)")
	records := fs.String("records", "", "override the product's default record location")
	_ = fs.Parse(os.Args[2:])
	if *product == "" || *agent == "" {
		fmt.Fprintln(os.Stderr, "agent-metrics: --product and --agent are required")
		fs.Usage()
		os.Exit(2)
	}
	a, err := adapter.New(*product)
	if err != nil {
		fatal(err)
	}
	path := *records
	if path == "" {
		path = expandHome(a.DefaultRecords())
	}
	events, warnings, err := a.Parse(path)
	if err != nil {
		fatal(err)
	}
	r := report.Collect(a.Name(), *agent, *execution, events, warnings)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		fatal(err)
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "agent-metrics:", err); os.Exit(1) }
