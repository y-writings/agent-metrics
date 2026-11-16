package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/y-writings/agent-metrics/internal/adapter"
	"github.com/y-writings/agent-metrics/internal/report"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[1] != "collect" {
		fmt.Fprintln(stderr, "usage: agent-metrics collect --product PRODUCT --agent AGENT_ID [--execution EXECUTION_ID] [--records PATH]")
		return 2
	}
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	product := fs.String("product", "", "claude-code, codex, or opencode-v2")
	agent := fs.String("agent", "", "identifier returned when the sub-agent was created")
	execution := fs.String("execution", "", "one execution identifier when the agent was reused")
	records := fs.String("records", "", "explicit record location; overrides product configuration and the default")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	if *product == "" || *agent == "" {
		fmt.Fprintln(stderr, "usage: agent-metrics collect --product PRODUCT --agent AGENT_ID [--execution EXECUTION_ID] [--records PATH]")
		return 2
	}
	a, err := adapter.New(*product)
	if err != nil {
		writeReport(stdout, unresolved("", *agent, "unsupported_product", "unsupported product"))
		return 1
	}
	source, err := adapter.ResolveSource(a.Name(), *records)
	if err != nil {
		code, message := "invalid_source", "record location could not be resolved"
		var sourceErr *adapter.SourceError
		if errors.As(err, &sourceErr) {
			code, message = sourceErr.Code, sourceErr.Message
		}
		writeReport(stdout, unresolved(a.Name(), *agent, code, message))
		return 1
	}
	result, err := a.Read(adapter.Request{Agent: *agent, Execution: *execution, Records: source})
	if err != nil {
		code, message := "read_error", "records could not be read"
		var sourceErr *adapter.SourceError
		if errors.As(err, &sourceErr) {
			code, message = sourceErr.Code, sourceErr.Message
		}
		writeReport(stdout, unresolved(a.Name(), *agent, code, message))
		return 1
	}
	collected := report.Collect(a.Name(), adapter.Request{Agent: *agent, Execution: *execution, Records: source}, result)
	writeReport(stdout, collected)
	if collected.Completeness == "incomplete" {
		return 1
	}
	return 0
}

func unresolved(product, agent, code, message string) report.ExecutionReport {
	return report.ExecutionReport{
		SchemaVersion: report.ExecutionSchemaVersion,
		Target:        report.Target{Product: product, AgentID: agent},
		Status:        "unknown",
		Completeness:  "incomplete",
		Usage: report.UsageScopes{
			Self:                []report.ModelUsage{},
			Subtree:             []report.ModelUsage{},
			SelfCompleteness:    "incomplete",
			SubtreeCompleteness: "incomplete",
		},
		Executions:        []report.Execution{},
		UnattributedUsage: []report.UnattributedUsage{},
		Diagnostics: []report.Diagnostic{{
			Code:    code,
			Message: message,
			Scope:   "source",
		}},
	}
}

func writeReport(stdout io.Writer, value report.ExecutionReport) {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
