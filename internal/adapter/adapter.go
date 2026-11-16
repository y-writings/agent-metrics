package adapter

import "strings"

// Adapter reads one product's native records. The common envelope parser is not
// a production input path.
type Adapter interface {
	Name() string
	Read(Request) (ReadResult, error)
}

func New(product string) (Adapter, error) {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "claude", "claude-code":
		return claudeCode{}, nil
	case "codex":
		return codex{}, nil
	case "opencode", "opencode-v2":
		return openCode{}, nil
	default:
		return nil, &SourceError{Code: "unsupported_product", Message: "unsupported product"}
	}
}

type claudeCode struct{}
type codex struct{}
type openCode struct{}

func (claudeCode) Name() string { return "claude-code" }
func (codex) Name() string      { return "codex" }
func (openCode) Name() string   { return "opencode-v2" }

func unsupported(product string) (ReadResult, error) {
	return ReadResult{Diagnostics: []Diagnostic{{
		Code:    "unsupported_source",
		Message: "native " + product + " records are not read yet",
		Scope:   "source",
	}}}, nil
}
