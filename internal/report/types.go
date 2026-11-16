package report

import "time"

const ExecutionSchemaVersion = "2.1"

type Tokens struct {
	Input      *int64 `json:"input"`
	CacheRead  *int64 `json:"cache_read"`
	CacheWrite *int64 `json:"cache_write"`
	Output     *int64 `json:"output"`
	Reasoning  *int64 `json:"reasoning"`
	Total      *int64 `json:"total"`
}

type ModelUsage struct {
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	Tokens   Tokens  `json:"tokens"`
}

type Timing struct {
	StartedAt           *time.Time `json:"started_at"`
	EndedAt             *time.Time `json:"ended_at"`
	ElapsedMilliseconds *int64     `json:"elapsed_milliseconds"`
	StartDefinition     string     `json:"start_definition"`
	EndDefinition       string     `json:"end_definition"`
	StartSourceID       *string    `json:"start_source_id"`
	EndSourceID         *string    `json:"end_source_id"`
}

// Diagnostic explains one completeness or attribution limit.
// Scope is source, agent, or execution. ExecutionID is omitted when the
// invocation itself is unknown.
type Diagnostic struct {
	Code           string  `json:"code"`
	Message        string  `json:"message"`
	Scope          string  `json:"scope,omitempty"`
	AgentID        string  `json:"agent_id,omitempty"`
	RelatedAgentID string  `json:"related_agent_id,omitempty"`
	ExecutionID    *string `json:"execution_id,omitempty"`
	Metric         string  `json:"metric,omitempty"`
	SourceID       string  `json:"source_id,omitempty"`
}

type Target struct {
	Product     string  `json:"product"`
	AgentID     string  `json:"agent_id"`
	ExecutionID *string `json:"execution_id"`
	SessionID   *string `json:"session_id"`
}

type UsageScopes struct {
	Self                []ModelUsage `json:"self"`
	Subtree             []ModelUsage `json:"subtree"`
	SelfCompleteness    string       `json:"self_completeness"`
	SubtreeCompleteness string       `json:"subtree_completeness"`
}

type Execution struct {
	ID                  string       `json:"id"`
	AgentID             string       `json:"agent_id"`
	ParentExecutionID   *string      `json:"parent_execution_id"`
	SpawnedAt           *time.Time   `json:"spawned_at"`
	StartedAt           *time.Time   `json:"started_at"`
	EndedAt             *time.Time   `json:"ended_at"`
	ElapsedMilliseconds *int64       `json:"elapsed_milliseconds"`
	Status              string       `json:"status"`
	Usage               []ModelUsage `json:"usage"`
	UsageCompleteness   string       `json:"usage_completeness"`
	SourceIDs           []string     `json:"source_ids"`
}

// UnattributedUsage holds counters whose execution could not be determined.
// They are not added to self or subtree.
type UnattributedUsage struct {
	AgentID  string  `json:"agent_id"`
	SourceID string  `json:"source_id"`
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	Tokens   Tokens  `json:"tokens"`
}

type ExecutionReport struct {
	SchemaVersion     string              `json:"schema_version"`
	Target            Target              `json:"target"`
	Status            string              `json:"status"`
	Completeness      string              `json:"completeness"`
	Timing            Timing              `json:"timing"`
	Usage             UsageScopes         `json:"usage"`
	Executions        []Execution         `json:"executions"`
	UnattributedUsage []UnattributedUsage `json:"unattributed_usage"`
	Diagnostics       []Diagnostic        `json:"diagnostics"`
}
