package adapter

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (claudeCode) Read(req Request) (ReadResult, error) {
	source := req.Records
	if source == "" {
		resolved, err := ResolveSource("claude-code", "")
		if err != nil {
			return ReadResult{}, err
		}
		source = resolved
	}
	files, err := claudeFiles(source)
	if err != nil {
		return ReadResult{}, err
	}
	if len(files) == 0 {
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "empty_source", Message: "record location contains no records", Scope: "source",
		}}}, nil
	}
	transcripts := make([]claudeTranscript, 0, len(files))
	for _, file := range files {
		transcripts = append(transcripts, parseClaudeFile(source, file))
	}
	agents, refs := buildClaudeAgents(transcripts)
	if len(agents[req.Agent]) == 0 && !claudeAgentReferenced(transcripts, req.Agent) {
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "agent_not_found", Message: "requested agent was not found", Scope: "agent", AgentID: req.Agent,
		}}}, nil
	}
	included := claudeIncludedAgents(req.Agent, transcripts)
	return buildClaudeResult(req.Agent, included, agents, refs, transcripts), nil
}

func claudeFiles(source string) ([]string, error) {
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &SourceError{Code: "invalid_source", Message: "explicit record location does not exist"}
		}
		return nil, &SourceError{Code: "invalid_source", Message: "record location is not readable"}
	}
	if !info.IsDir() {
		return []string{source}, nil
	}
	var files []string
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(path)) != ".jsonl" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, &SourceError{Code: "invalid_source", Message: "record location is not readable"}
	}
	sort.Strings(files)
	return files, nil
}

type claudeTranscript struct {
	rel, agentID, sessionID, parentAgent, spawnTool string
	records                                         []claudeRecord
	truncated                                       bool
}

type claudeRecord struct {
	line, index                          int
	time                                 time.Time
	kind, uuid, agentID, sessionID       string
	origin, messageID, model, stopReason string
	requestID, toolName, toolID, sendTo  string
	toolAgent, toolStatus                string
	isMeta, toolEnds, interrupt, unknown bool
	toolResult                           bool
	usage                                *claudeCounters
	noteAgent, noteTool, noteStatus      string
}

type claudeCounters struct {
	input, cacheRead, cacheWrite, output, reasoning, total *int64
}

type claudeExecution struct {
	id, agent, session, parentExec, status, model string
	start, end                                    time.Time
	startID, endID                                string
	startDef                                      string
	spawn, confirmed, terminal, partial           bool
	usage                                         []claudeRecord
}

type claudeNote struct {
	agent, toolID, status, source string
	time                          time.Time
}

type claudeRef struct {
	child, parentExec, parentAgent, toolID string
	time                                   time.Time
}

func parseClaudeFile(root, path string) claudeTranscript {
	transcript := claudeTranscript{rel: claudeRel(root, path), agentID: claudeAgentFromPath(path)}
	metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
	if meta, err := os.ReadFile(metaPath); err == nil {
		var decoded struct {
			ParentAgentID string `json:"parentAgentId"`
			ToolUseID     string `json:"toolUseId"`
		}
		if json.Unmarshal(meta, &decoded) == nil {
			transcript.parentAgent = decoded.ParentAgentID
			transcript.spawnTool = decoded.ToolUseID
		}
	}
	handle, err := os.Open(path)
	if err != nil {
		transcript.truncated = true
		return transcript
	}
	defer handle.Close()
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		record, ok := parseClaudeLine(lineNo, text)
		if !ok {
			transcript.truncated = true
			break
		}
		record.index = len(transcript.records)
		if record.agentID != "" && transcript.agentID == "" {
			transcript.agentID = record.agentID
		}
		if record.sessionID != "" && transcript.sessionID == "" {
			transcript.sessionID = record.sessionID
		}
		transcript.records = append(transcript.records, record)
	}
	if scanner.Err() != nil {
		transcript.truncated = true
	}
	return transcript
}

func parseClaudeLine(line int, raw string) (claudeRecord, bool) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return claudeRecord{}, false
	}
	kind, _ := decoded["type"].(string)
	if kind == "" {
		return claudeRecord{}, false
	}
	record := claudeRecord{line: line, kind: kind, uuid: strField(decoded, "uuid"), agentID: strField(decoded, "agentId"), sessionID: firstString(decoded, "sessionId", "session_id"), requestID: strField(decoded, "requestId")}
	if value, ok := decoded["isMeta"].(bool); ok {
		record.isMeta = value
	}
	if value, ok := decoded["toolEndsTurn"].(bool); ok {
		record.toolEnds = value
	}
	if stamp := strField(decoded, "timestamp"); stamp != "" {
		record.time, _ = time.Parse(time.RFC3339Nano, stamp)
	}
	if origin, ok := decoded["origin"].(map[string]any); ok {
		record.origin = strField(origin, "kind")
	}
	if !knownClaudeKind(kind) {
		record.unknown = true
		return record, true
	}
	if message, ok := decoded["message"].(map[string]any); ok {
		record.messageID = strField(message, "id")
		record.model = strField(message, "model")
		record.stopReason = strField(message, "stop_reason")
		record.interrupt = interruptText(message["content"])
		record.toolResult = contentHasType(message["content"], "tool_result")
		record.usage = claudeUsage(message["usage"])
		if name, id, to := firstToolUse(message["content"]); name != "" {
			record.toolName, record.toolID, record.sendTo = name, id, to
		}
		if record.toolID == "" {
			record.toolID = toolResultID(message["content"])
		}
	}
	if result, ok := decoded["toolUseResult"].(map[string]any); ok {
		record.toolAgent = strField(result, "agentId")
		record.toolStatus = strField(result, "status")
	}
	if kind == "queue-operation" && strField(decoded, "operation") == "enqueue" {
		record.noteAgent, record.noteTool, record.noteStatus = notificationFields(strField(decoded, "content"))
	}
	if record.origin == "task-notification" {
		if message, ok := decoded["message"].(map[string]any); ok {
			record.noteAgent, record.noteTool, record.noteStatus = notificationFields(contentText(message["content"]))
		}
	}
	if record.toolAgent != "" && (record.toolStatus == "completed" || record.toolStatus == "killed") {
		record.noteAgent = record.toolAgent
		record.noteStatus = record.toolStatus
		record.noteTool = record.toolID
	}
	if kind == "api-request" {
		record.requestID = strField(decoded, "id")
	}
	return record, true
}

func knownClaudeKind(kind string) bool {
	switch kind {
	case "user", "assistant", "attachment", "api-request", "queue-operation":
		return true
	default:
		return false
	}
}

func claudeUsage(value any) *claudeCounters {
	usage, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	counters := &claudeCounters{
		input:      intField(usage["input_tokens"]),
		cacheWrite: intField(usage["cache_creation_input_tokens"]),
		cacheRead:  intField(usage["cache_read_input_tokens"]),
		output:     intField(usage["output_tokens"]),
	}
	if details, ok := usage["output_tokens_details"].(map[string]any); ok {
		counters.reasoning = intField(details["thinking_tokens"])
	}
	if counters.input != nil && counters.cacheWrite != nil && counters.cacheRead != nil && counters.output != nil {
		total := *counters.input + *counters.cacheWrite + *counters.cacheRead + *counters.output
		counters.total = &total
	}
	if counters.input == nil && counters.cacheWrite == nil && counters.cacheRead == nil && counters.output == nil && counters.reasoning == nil {
		return nil
	}
	return counters
}

func buildClaudeAgents(transcripts []claudeTranscript) (map[string][]claudeExecution, map[string][]claudeRef) {
	notes := claudeNotes(transcripts)
	agents := map[string][]claudeExecution{}
	for _, transcript := range transcripts {
		if transcript.agentID == "" || !claudeSubagentTranscript(transcript.rel) {
			continue
		}
		executions := claudeSegments(transcript)
		confirmClaudeExecutions(executions, notes[transcript.agentID], transcript.spawnTool)
		for index := range executions {
			executions[index].session = transcript.sessionID
		}
		agents[transcript.agentID] = append(agents[transcript.agentID], executions...)
	}
	refs := claudeReferences(transcripts)
	for agent, executions := range agents {
		matched := refs[agent]
		sort.Slice(executions, func(i, j int) bool { return executions[i].start.Before(executions[j].start) })
		if len(matched) == len(executions) {
			for i := range executions {
				executions[i].parentExec = matched[i].parentExec
			}
		}
		agents[agent] = executions
	}
	return agents, refs
}

func claudeSegments(transcript claudeTranscript) []claudeExecution {
	starts := []int{}
	seenStart := false
	for index, record := range transcript.records {
		if record.kind != "user" {
			continue
		}
		if record.origin == "coordinator" || (!seenStart && !record.isMeta && !record.toolResult && record.origin == "" && !record.interrupt) {
			starts = append(starts, index)
			seenStart = true
		}
	}
	var executions []claudeExecution
	for nth, start := range starts {
		end := len(transcript.records)
		if nth+1 < len(starts) {
			end = starts[nth+1]
		}
		records := transcript.records[start:end]
		execution := claudeExecution{
			id: records[0].uuid, agent: transcript.agentID, start: records[0].time, startID: sourceLine(transcript.rel, records[0].line),
			startDef: "subagent invocation user timestamp", status: "running", spawn: records[0].origin != "coordinator",
		}
		var last *claudeRecord
		pendingRequest := false
		for i := range records {
			record := &records[i]
			if isClaudeTerminal(*record) {
				last = record
			}
			if record.kind == "api-request" {
				if pendingRequest {
					execution.partial = true
				}
				pendingRequest = true
			}
			if record.kind == "assistant" {
				pendingRequest = false
			}
			if record.usage != nil && record.messageID != "" {
				execution.usage = append(execution.usage, *record)
			}
		}
		if pendingRequest {
			execution.partial = true
		}
		if last != nil {
			execution.terminal = true
			execution.end = last.time
			execution.endID = sourceLine(transcript.rel, last.line)
			if last.interrupt {
				execution.status = "interrupted"
			}
		}
		executions = append(executions, execution)
	}
	return executions
}

func confirmClaudeExecutions(executions []claudeExecution, notes []claudeNote, spawnTool string) {
	if len(executions) == 0 {
		return
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].time.Equal(notes[j].time) {
			return notes[i].source < notes[j].source
		}
		return notes[i].time.Before(notes[j].time)
	})
	used := map[int]bool{}
	remaining := append([]claudeExecution(nil), executions...)
	if executions[0].spawn && spawnTool != "" {
		for index, note := range notes {
			if note.toolID == spawnTool {
				executions[0].confirmed = true
				executions[0].status = claudeStopStatus(note.status, executions[0].status)
				used[index] = true
				remaining = remaining[1:]
				break
			}
		}
	}
	var others []claudeNote
	for index, note := range notes {
		if used[index] || (spawnTool != "" && note.toolID == spawnTool) {
			continue
		}
		others = append(others, note)
	}
	if len(others) != len(remaining) {
		return
	}
	for index := range remaining {
		for execIndex := range executions {
			if executions[execIndex].id == remaining[index].id {
				executions[execIndex].confirmed = true
				executions[execIndex].status = claudeStopStatus(others[index].status, executions[execIndex].status)
			}
		}
	}
}

func claudeNotes(transcripts []claudeTranscript) map[string][]claudeNote {
	seenTool := map[string]bool{}
	notes := map[string][]claudeNote{}
	for _, transcript := range transcripts {
		for _, record := range transcript.records {
			if record.noteAgent == "" || (record.noteStatus != "completed" && record.noteStatus != "killed") {
				continue
			}
			if record.noteTool != "" {
				key := record.noteAgent + "|" + record.noteTool + "|" + record.noteStatus
				if seenTool[key] {
					continue
				}
				seenTool[key] = true
			}
			notes[record.noteAgent] = append(notes[record.noteAgent], claudeNote{
				agent: record.noteAgent, toolID: record.noteTool, status: record.noteStatus,
				time: record.time, source: sourceLine(transcript.rel, record.line),
			})
		}
	}
	return notes
}

func claudeReferences(transcripts []claudeTranscript) map[string][]claudeRef {
	refs := map[string][]claudeRef{}
	for _, transcript := range transcripts {
		execID := func(index int) string {
			starts := segmentStarts(transcript)
			var current string
			for _, start := range starts {
				if start <= index {
					current = transcript.records[start].uuid
				}
			}
			return current
		}
		for index, record := range transcript.records {
			child := ""
			if record.toolName == "SendMessage" && record.sendTo != "" {
				child = record.sendTo
			}
			if record.toolAgent != "" && (record.toolStatus == "async_launched" || record.toolStatus == "completed") {
				child = record.toolAgent
			}
			if child == "" {
				continue
			}
			refs[child] = append(refs[child], claudeRef{child: child, parentExec: execID(index), parentAgent: transcript.agentID, toolID: record.toolID, time: record.time})
		}
	}
	for child := range refs {
		refs[child] = dedupeClaudeRefs(refs[child])
		sort.Slice(refs[child], func(i, j int) bool {
			if refs[child][i].time.Equal(refs[child][j].time) {
				return refs[child][i].parentExec < refs[child][j].parentExec
			}
			return refs[child][i].time.Before(refs[child][j].time)
		})
	}
	return refs
}

func segmentStarts(transcript claudeTranscript) []int {
	var starts []int
	seen := false
	for index, record := range transcript.records {
		if record.kind == "user" && (record.origin == "coordinator" || (!seen && !record.isMeta && !record.toolResult && record.origin == "" && !record.interrupt)) {
			starts = append(starts, index)
			seen = true
		}
	}
	return starts
}

func buildClaudeResult(target string, included map[string]bool, agents map[string][]claudeExecution, refs map[string][]claudeRef, transcripts []claudeTranscript) ReadResult {
	result := ReadResult{AgentID: target}
	for _, transcript := range transcripts {
		if transcript.agentID == "" || !included[transcript.agentID] {
			continue
		}
		if transcript.truncated {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "truncated_record", Message: "a record file is truncated", Scope: "agent", AgentID: transcript.agentID, SourceID: transcript.rel,
			})
		}
		for _, record := range transcript.records {
			if !record.unknown {
				continue
			}
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "unsupported_record", Message: "an unsupported record type was ignored", Scope: "agent", AgentID: transcript.agentID, SourceID: sourceLine(transcript.rel, record.line),
			})
		}
	}
	for agent := range included {
		for _, execution := range agents[agent] {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "unknown_provider", Message: "saved transcript has no provider", Scope: "execution", AgentID: agent, Metric: "provider",
				Execution: &ExecutionRef{AgentID: agent, ExecutionID: execution.id},
			})
			result.Events = append(result.Events, Event{
				ID: execution.startID, Session: agent, SourceSession: execution.session, Execution: execution.id,
				ParentExecution: execution.parentExec, Kind: "execution_start", Status: "running", Time: execution.start,
				StartDefinition: execution.startDef,
			})
			for _, usage := range dedupeClaudeUsage(execution.usage) {
				result.Events = append(result.Events, Event{
					ID: sourceLine(usageSource(transcripts, agent), usage.line), UsageID: usage.messageID, Session: agent, SourceSession: execution.session,
					Execution: execution.id, Kind: "usage", Model: usage.model,
					Input: usage.usage.input, CacheRead: usage.usage.cacheRead, CacheWrite: usage.usage.cacheWrite,
					Output: usage.usage.output, Reasoning: usage.usage.reasoning, Total: usage.usage.total,
				})
			}
			if execution.partial {
				result.Events = append(result.Events, Event{ID: execution.id + ":unanswered", Session: agent, Execution: execution.id, Kind: "usage"})
			}
			if execution.confirmed && execution.terminal {
				result.Events = append(result.Events, Event{
					ID: execution.endID, Session: agent, SourceSession: execution.session, Execution: execution.id,
					ParentExecution: execution.parentExec, Kind: "execution_end", Status: execution.status, Time: execution.end,
					EndDefinition: "last confirmed loop terminal timestamp",
				})
			}
		}
		if len(agents[agent]) == 0 {
			for _, transcript := range transcripts {
				if transcript.agentID != agent {
					continue
				}
				for _, usage := range dedupeClaudeUsage(usageRecords(transcript.records)) {
					result.Events = append(result.Events, Event{
						ID: sourceLine(transcript.rel, usage.line), UsageID: usage.messageID, Session: agent, SourceSession: transcript.sessionID,
						Kind: "usage", Model: usage.model,
						Input: usage.usage.input, CacheRead: usage.usage.cacheRead, CacheWrite: usage.usage.cacheWrite,
						Output: usage.usage.output, Reasoning: usage.usage.reasoning, Total: usage.usage.total,
					})
				}
			}
		}
	}
	for child, matched := range refs {
		if claudeChildLinked(agents[child]) {
			continue
		}
		for _, ref := range matched {
			if ref.parentAgent == "" || !included[ref.parentAgent] || ref.parentExec == "" {
				continue
			}
			result.Delegations = append(result.Delegations, Delegation{
				ParentAgentID: ref.parentAgent,
				ChildAgentID:  child,
				Parent:        &ExecutionRef{AgentID: ref.parentAgent, ExecutionID: ref.parentExec},
			})
		}
	}
	return result
}

func claudeChildLinked(executions []claudeExecution) bool {
	if len(executions) == 0 {
		return false
	}
	for _, execution := range executions {
		if execution.parentExec == "" {
			return false
		}
	}
	return true
}

func dedupeClaudeRefs(refs []claudeRef) []claudeRef {
	seen := map[string]bool{}
	var unique []claudeRef
	for _, ref := range refs {
		key := ref.child + "|" + ref.parentExec + "|" + ref.time.UTC().Format(time.RFC3339Nano) + "|" + ref.toolID
		if ref.toolID != "" {
			key = ref.child + "|" + ref.toolID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, ref)
	}
	return unique
}

func toolResultID(content any) string {
	items, ok := content.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok || strField(block, "type") != "tool_result" {
			continue
		}
		if id := strField(block, "tool_use_id"); id != "" {
			return id
		}
	}
	return ""
}

func claudeIncludedAgents(target string, transcripts []claudeTranscript) map[string]bool {
	parent := map[string]string{}
	for _, transcript := range transcripts {
		if transcript.agentID != "" {
			parent[transcript.agentID] = transcript.parentAgent
		}
	}
	included := map[string]bool{target: true}
	for changed := true; changed; {
		changed = false
		for agent, parentAgent := range parent {
			if included[agent] || parentAgent == "" || !included[parentAgent] {
				continue
			}
			included[agent] = true
			changed = true
		}
	}
	return included
}

func claudeAgentReferenced(transcripts []claudeTranscript, agent string) bool {
	for _, transcript := range transcripts {
		if transcript.agentID == agent {
			return true
		}
		for _, record := range transcript.records {
			if record.toolAgent == agent || record.sendTo == agent || record.noteAgent == agent {
				return true
			}
		}
	}
	return false
}

func dedupeClaudeUsage(records []claudeRecord) []claudeRecord {
	order := []string{}
	groups := map[string][]claudeRecord{}
	for _, record := range records {
		id := record.messageID
		if id == "" {
			id = record.uuid
		}
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] = append(groups[id], record)
	}
	var out []claudeRecord
	for _, id := range order {
		group := groups[id]
		same := true
		for _, record := range group[1:] {
			if !sameCounters(group[0].usage, record.usage) || group[0].model != record.model {
				same = false
				break
			}
		}
		if same {
			out = append(out, group[0])
			continue
		}
		out = append(out, group...)
	}
	return out
}

func sameCounters(a, b *claudeCounters) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return sameInt64(a.input, b.input) && sameInt64(a.cacheRead, b.cacheRead) && sameInt64(a.cacheWrite, b.cacheWrite) && sameInt64(a.output, b.output) && sameInt64(a.reasoning, b.reasoning) && sameInt64(a.total, b.total)
}

func sameInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func usageRecords(records []claudeRecord) []claudeRecord {
	var out []claudeRecord
	for _, record := range records {
		if record.usage != nil && record.kind == "assistant" {
			out = append(out, record)
		}
	}
	return out
}

func usageSource(transcripts []claudeTranscript, agent string) string {
	for _, transcript := range transcripts {
		if transcript.agentID == agent {
			return transcript.rel
		}
	}
	return agent
}

func isClaudeTerminal(record claudeRecord) bool {
	if record.kind == "assistant" && record.stopReason == "end_turn" {
		return true
	}
	if record.toolEnds || record.interrupt {
		return true
	}
	return false
}

func claudeStopStatus(note, current string) string {
	switch note {
	case "killed":
		return "interrupted"
	case "completed":
		if current == "interrupted" {
			return "interrupted"
		}
		return "completed"
	default:
		if current != "" {
			return current
		}
		return "completed"
	}
}

func notificationFields(text string) (agent, tool, status string) {
	agent = tagged(text, "task-id")
	tool = tagged(text, "tool-use-id")
	status = tagged(text, "status")
	if status != "completed" && status != "killed" {
		return "", "", ""
	}
	return agent, tool, status
}

func tagged(text, tag string) string {
	start := strings.Index(text, "<"+tag+">")
	end := strings.Index(text, "</"+tag+">")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	value := text[start+len(tag)+2 : end]
	if strings.ContainsAny(value, "<>\n") || len(value) > 80 {
		return ""
	}
	return value
}

func interruptText(content any) bool {
	switch value := content.(type) {
	case string:
		return strings.Contains(value, "[Request interrupted by user]")
	case []any:
		for _, item := range value {
			if interruptText(item) {
				return true
			}
		}
	case map[string]any:
		return interruptText(value["text"]) || interruptText(value["content"])
	}
	return false
}

func contentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, item := range value {
			builder.WriteString(contentText(item))
		}
		return builder.String()
	case map[string]any:
		return contentText(value["text"])
	default:
		return ""
	}
}

func contentHasType(content any, kind string) bool {
	items, ok := content.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		block, ok := item.(map[string]any)
		if ok && strField(block, "type") == kind {
			return true
		}
	}
	return false
}

func firstToolUse(content any) (name, id, to string) {
	items, ok := content.([]any)
	if !ok {
		return "", "", ""
	}
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok || strField(block, "type") != "tool_use" {
			continue
		}
		name = strField(block, "name")
		id = strField(block, "id")
		if input, ok := block["input"].(map[string]any); ok {
			to = strField(input, "to")
		}
		return name, id, to
	}
	return "", "", ""
}

func claudeSubagentTranscript(rel string) bool {
	base := filepath.Base(rel)
	return strings.Contains(rel, "/subagents/") || (strings.HasPrefix(base, "agent-") && strings.HasSuffix(base, ".jsonl"))
}

func claudeAgentFromPath(path string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "agent-") && strings.HasSuffix(base, ".jsonl") {
		return strings.TrimSuffix(strings.TrimPrefix(base, "agent-"), ".jsonl")
	}
	return ""
}

func claudeRel(root, path string) string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return filepath.Base(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

func sourceLine(rel string, line int) string {
	return rel + ":" + strconv.Itoa(line)
}

func strField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strField(values, key); value != "" {
			return value
		}
	}
	return ""
}

func intField(value any) *int64 {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	parsed := int64(number)
	return &parsed
}
