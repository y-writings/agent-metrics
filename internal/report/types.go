package report

import "time"

const SchemaVersion = "1.0"

type Tokens struct {
	Input      *int64 `json:"input"`
	CacheRead  *int64 `json:"cache_read"`
	CacheWrite *int64 `json:"cache_write"`
	Output     *int64 `json:"output"`
	Reasoning  *int64 `json:"reasoning"`
	Total      *int64 `json:"total"`
}

type ModelUsage struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Tokens   Tokens `json:"tokens"`
}

type Agent struct {
	ID        string       `json:"id"`
	ParentID  *string      `json:"parent_id"`
	SpawnedAt *time.Time   `json:"spawned_at"`
	Status    string       `json:"status"`
	Usage     []ModelUsage `json:"usage"`
	SourceIDs []string     `json:"source_ids"`
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

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Report struct {
	SchemaVersion string                            `json:"schema_version"`
	Product       string                            `json:"product"`
	Task          struct{ ParentID, TaskID string } `json:"task"`
	Status        string                            `json:"status"`
	Completeness  string                            `json:"completeness"`
	Timing        Timing                            `json:"timing"`
	Usage         []ModelUsage                      `json:"usage"`
	Agents        []Agent                           `json:"agents"`
	Diagnostics   []Diagnostic                      `json:"diagnostics"`
}
