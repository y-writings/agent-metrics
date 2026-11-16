package adapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

const openCodeFreshSessionWindow = int64(10 * time.Second / time.Millisecond)

func (openCode) Read(req Request) (ReadResult, error) {
	source := req.Records
	if source == "" {
		resolved, err := ResolveSource("opencode-v2", "")
		if err != nil {
			return ReadResult{}, err
		}
		source = resolved
	}
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return ReadResult{}, &SourceError{Code: "invalid_source", Message: "explicit record location does not exist"}
		}
		return ReadResult{}, &SourceError{Code: "invalid_source", Message: "record location is not readable"}
	}
	if info.IsDir() {
		return ReadResult{}, &SourceError{Code: "invalid_source", Message: "OpenCode records must be a database file"}
	}
	sessions, messages, err := readOpenCodeDB(source)
	if err != nil {
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "malformed_record", Message: "the database could not be read", Scope: "source",
		}}}, nil
	}
	if _, ok := sessions[req.Agent]; !ok {
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "agent_not_found", Message: "requested agent was not found", Scope: "agent", AgentID: req.Agent,
		}}}, nil
	}
	included := openCodeDescendants(req.Agent, sessions)
	return buildOpenCodeResult(req.Agent, included, sessions, messages), nil
}

type openCodeSession struct {
	id, parent, model, provider string
	created                     int64
	cumulative                  openCodeCounters
}

type openCodeMessage struct {
	id, session, kind, responseID, model, provider string
	seq                                            int
	created                                        int64
	outcome                                        string
	tokens                                         *openCodeCounters
	childSessions                                  []string
	unknown                                        bool
}

type openCodeCounters struct {
	input, output, reasoning, cacheRead, cacheWrite *int64
}

type openCodeExecution struct {
	id, session, parentExec, status string
	start, end                      int64
	startID, endID                  string
	usage                           []openCodeMessage
	confirmed                       bool
	fresh                           bool
}

func readOpenCodeDB(path string) (map[string]openCodeSession, []openCodeMessage, error) {
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	sessions, err := queryOpenCodeSessions(tx)
	if err != nil {
		return nil, nil, err
	}
	messages, err := queryOpenCodeMessages(tx)
	if err != nil {
		return nil, nil, err
	}
	return sessions, messages, nil
}

func queryOpenCodeSessions(tx *sql.Tx) (map[string]openCodeSession, error) {
	rows, err := tx.Query(`SELECT id, parent_id, model, time_created, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write FROM session_v2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := map[string]openCodeSession{}
	for rows.Next() {
		var session openCodeSession
		var parent, model sql.NullString
		var created sql.NullInt64
		var input, output, reasoning, cacheRead, cacheWrite sql.NullInt64
		if err := rows.Scan(&session.id, &parent, &model, &created, &input, &output, &reasoning, &cacheRead, &cacheWrite); err != nil {
			return nil, err
		}
		session.parent = parent.String
		session.created = created.Int64
		session.model, session.provider = openCodeModel(model.String)
		session.cumulative = openCodeCounters{
			input: nullInt(input), output: nullInt(output), reasoning: nullInt(reasoning),
			cacheRead: nullInt(cacheRead), cacheWrite: nullInt(cacheWrite),
		}
		sessions[session.id] = session
	}
	return sessions, rows.Err()
}

func queryOpenCodeMessages(tx *sql.Tx) ([]openCodeMessage, error) {
	rows, err := tx.Query(`SELECT id, session_id, type, seq, time_created, data FROM session_message ORDER BY session_id, seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []openCodeMessage
	for rows.Next() {
		var id, sessionID, kind, data string
		var seq int
		var created int64
		if err := rows.Scan(&id, &sessionID, &kind, &seq, &created, &data); err != nil {
			return nil, err
		}
		message := openCodeMessage{id: id, session: sessionID, kind: kind, seq: seq, created: created}
		if !knownOpenCodeMessage(kind) {
			message.unknown = true
			messages = append(messages, message)
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(data), &payload) != nil {
			message.unknown = true
			messages = append(messages, message)
			continue
		}
		if stamp := nestedInt(payload, "time", "created"); stamp != nil {
			message.created = *stamp
		}
		message.outcome, _ = payload["outcome"].(string)
		message.model, message.provider = openCodeModelValue(payload["model"])
		if native, ok := payload["native"].(map[string]any); ok {
			message.responseID, _ = native["responseId"].(string)
		}
		message.tokens = openCodeTokens(payload["tokens"])
		message.childSessions = subagentSessions(payload["content"])
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func buildOpenCodeResult(target string, included map[string]bool, sessions map[string]openCodeSession, messages []openCodeMessage) ReadResult {
	result := ReadResult{AgentID: target}
	executions := map[string][]openCodeExecution{}
	bySession := map[string][]openCodeMessage{}
	for _, message := range messages {
		if !included[message.session] {
			continue
		}
		bySession[message.session] = append(bySession[message.session], message)
		if message.unknown {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "unsupported_record", Message: "an unsupported record type was ignored", Scope: "agent", AgentID: message.session, SourceID: message.id,
			})
		}
	}
	for sessionID, sessionMessages := range bySession {
		executions[sessionID] = openCodeExecutions(sessions[sessionID], sessionMessages)
	}
	for sessionID, sessionExecutions := range executions {
		for index := range sessionExecutions {
			execution := &sessionExecutions[index]
			execution.parentExec = openCodeParentFor(sessionExecutions, index, executions, messages)
			result.Events = append(result.Events, Event{
				ID: execution.startID, Session: sessionID, SourceSession: sessionID, Execution: execution.id,
				ParentExecution: execution.parentExec, Kind: "execution_start", Status: "running", Time: millis(execution.start),
				StartDefinition: "subagent user message time.created",
			})
			for _, usage := range execution.usage {
				result.Events = append(result.Events, Event{
					ID: usage.id, UsageID: openCodeUsageID(usage), Session: sessionID, SourceSession: sessionID,
					Execution: execution.id, Kind: "usage", Provider: usage.provider, Model: usage.model,
					Input: usage.tokens.input, Output: usage.tokens.output, Reasoning: usage.tokens.reasoning,
					CacheRead: usage.tokens.cacheRead, CacheWrite: usage.tokens.cacheWrite,
				})
			}
			if execution.confirmed {
				result.Events = append(result.Events, Event{
					ID: execution.endID, Session: sessionID, SourceSession: sessionID, Execution: execution.id,
					ParentExecution: execution.parentExec, Kind: "execution_end", Status: execution.status, Time: millis(execution.end),
					EndDefinition: "subagent idle time.created",
				})
			}
		}
	}
	for _, message := range messages {
		if len(message.childSessions) == 0 || !included[message.session] {
			continue
		}
		parentExec := executionAt(executions[message.session], message.seq, bySession[message.session])
		for _, child := range message.childSessions {
			if len(executions[child]) > 0 {
				continue
			}
			result.Delegations = append(result.Delegations, Delegation{
				ParentAgentID: message.session, ChildAgentID: child, SourceID: message.id,
				Parent: &ExecutionRef{AgentID: message.session, ExecutionID: parentExec},
			})
		}
	}
	return result
}

func openCodeExecutions(session openCodeSession, messages []openCodeMessage) []openCodeExecution {
	users := 0
	for _, message := range messages {
		if message.kind == "user" {
			users++
		}
	}
	var executions []openCodeExecution
	var current *openCodeExecution
	for _, message := range messages {
		switch message.kind {
		case "user":
			if current != nil {
				executions = append(executions, *current)
			}
			current = &openCodeExecution{
				id: message.id, session: message.session, start: message.created, startID: message.id, status: "running",
				fresh: users == 1 && message.created >= session.created && message.created-session.created <= openCodeFreshSessionWindow,
			}
		case "assistant":
			if current == nil || message.tokens == nil {
				continue
			}
			current.usage = append(current.usage, message)
		case "idle":
			if current == nil || current.confirmed {
				continue
			}
			current.confirmed = true
			current.end = message.created
			current.endID = message.id
			current.status = message.outcome
			if current.status == "" {
				current.status = "succeeded"
			}
		}
	}
	if current != nil {
		executions = append(executions, *current)
	}
	for index := range executions {
		if len(executions[index].usage) > 0 || !executions[index].fresh {
			continue
		}
		if !openCodeCountersPresent(session.cumulative) {
			continue
		}
		executions[index].usage = []openCodeMessage{{
			id: executions[index].id + ":baseline", session: executions[index].session,
			model: session.model, provider: session.provider, tokens: &session.cumulative,
		}}
	}
	return executions
}

func openCodeParentFor(childExecs []openCodeExecution, index int, executions map[string][]openCodeExecution, messages []openCodeMessage) string {
	if index < 0 || index >= len(childExecs) {
		return ""
	}
	child := childExecs[index].session
	var refs []string
	for _, message := range messages {
		for _, id := range message.childSessions {
			if id != child {
				continue
			}
			parentExec := executionAt(executions[message.session], message.seq, messagesForSession(messages, message.session))
			if parentExec != "" {
				refs = append(refs, parentExec)
			}
		}
	}
	if len(refs) == len(childExecs) {
		return refs[index]
	}
	return ""
}

func messagesForSession(messages []openCodeMessage, session string) []openCodeMessage {
	var selected []openCodeMessage
	for _, message := range messages {
		if message.session == session {
			selected = append(selected, message)
		}
	}
	return selected
}

func executionAt(executions []openCodeExecution, seq int, messages []openCodeMessage) string {
	if len(executions) == 1 {
		return executions[0].id
	}
	// Messages are ordered. The execution that contains seq is the latest user at or before it.
	var current string
	for _, execution := range executions {
		current = execution.id
	}
	if messages == nil {
		return current
	}
	current = ""
	for _, message := range messages {
		if message.seq > seq {
			break
		}
		if message.kind == "user" {
			current = message.id
		}
	}
	return current
}

func openCodeDescendants(target string, sessions map[string]openCodeSession) map[string]bool {
	included := map[string]bool{target: true}
	for changed := true; changed; {
		changed = false
		for id, session := range sessions {
			if included[id] || session.parent == "" || !included[session.parent] {
				continue
			}
			included[id] = true
			changed = true
		}
	}
	return included
}

func openCodeTokens(value any) *openCodeCounters {
	raw, ok := value.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	counters := &openCodeCounters{
		input: intAny(raw["input"]), output: intAny(raw["output"]), reasoning: intAny(raw["reasoning"]),
	}
	if cache, ok := raw["cache"].(map[string]any); ok {
		counters.cacheRead = intAny(cache["read"])
		counters.cacheWrite = intAny(cache["write"])
	}
	if counters.input == nil && counters.output == nil && counters.reasoning == nil && counters.cacheRead == nil && counters.cacheWrite == nil {
		return nil
	}
	return counters
}

func openCodeCountersPresent(counters openCodeCounters) bool {
	return counters.input != nil || counters.output != nil || counters.reasoning != nil || counters.cacheRead != nil || counters.cacheWrite != nil
}

func subagentSessions(content any) []string {
	items, ok := content.([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok || block["type"] != "tool" || block["name"] != "subagent" {
			continue
		}
		state, _ := block["state"].(map[string]any)
		if metadata, ok := state["metadata"].(map[string]any); ok {
			if id, _ := metadata["sessionID"].(string); id != "" {
				ids = append(ids, id)
				continue
			}
		}
		if input, ok := state["input"].(map[string]any); ok {
			if id, _ := input["sessionID"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func openCodeModel(raw string) (model, provider string) {
	if raw == "" {
		return "", ""
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(raw), &decoded) != nil {
		return "", ""
	}
	return openCodeModelValue(decoded)
}

func openCodeModelValue(value any) (model, provider string) {
	decoded, ok := value.(map[string]any)
	if !ok {
		return "", ""
	}
	model, _ = decoded["id"].(string)
	provider, _ = decoded["providerID"].(string)
	return model, provider
}

func openCodeUsageID(message openCodeMessage) string {
	if message.responseID != "" {
		return message.responseID
	}
	return message.id
}

func knownOpenCodeMessage(kind string) bool {
	switch kind {
	case "user", "assistant", "idle":
		return true
	default:
		return false
	}
}

func nestedInt(payload map[string]any, outer, inner string) *int64 {
	child, ok := payload[outer].(map[string]any)
	if !ok {
		return nil
	}
	return intAny(child[inner])
}

func intAny(value any) *int64 {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	parsed := int64(number)
	return &parsed
}

func nullInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	parsed := value.Int64
	return &parsed
}

func millis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}
