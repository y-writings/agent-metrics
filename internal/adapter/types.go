package adapter

import "time"

// Request is the caller-facing selection. Agent is the identifier returned when
// the sub-agent was created. Execution is one invocation of that agent, when
// the caller already knows which reuse to measure.
type Request struct {
	Agent, Execution, Records string
}

// ExecutionRef is an internal invocation identity. AgentID is the native
// agent, thread, or session. ExecutionID is that agent's invocation, never a
// fabricated substitute for a missing native id.
type ExecutionRef struct {
	AgentID, ExecutionID string
}

// Delegation records an explicit parent-to-child link. A nil Parent or Child
// means that side's invocation was not resolved; the agent ids stay populated.
type Delegation struct {
	ParentAgentID, ChildAgentID, SourceID string
	Parent, Child                         *ExecutionRef
}

// Diagnostic is a reader finding. Scope is source, agent, or execution.
// A nil Execution with Scope agent is an agent-level finding, not a
// source-wide one.
type Diagnostic struct {
	Code, Message, SourceID, Metric string
	Scope, AgentID, RelatedAgentID  string
	Execution                       *ExecutionRef
}

// Event is metadata for one native record. It never carries prompt, message,
// tool, or credential bodies. ID is the source record. UsageID is the
// consumption identity used to count a response or message once.
type Event struct {
	ID, UsageID, Session, SourceSession, Parent, Task      string
	Kind, Provider, Model, Status                          string
	Execution, ParentExecution, ReferencedExecution        string
	Time                                                   time.Time
	Input, CacheRead, CacheWrite, Output, Reasoning, Total *int64
	Cumulative                                             bool
	StartDefinition, EndDefinition                         string
}

// ReadResult is the normalized slice of a native source that a product reader
// could relate to the requested agent. AgentID is the resolved native
// identity, which may differ from the returned reference in Request.Agent.
type ReadResult struct {
	AgentID     string
	Events      []Event
	Delegations []Delegation
	Diagnostics []Diagnostic
}

// SourceError is a safe resolution failure. Message is written for operators
// and must not echo OS, JSON, or SQL error text.
type SourceError struct {
	Code, Message string
}

func (e *SourceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}
