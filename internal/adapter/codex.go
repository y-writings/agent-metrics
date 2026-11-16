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

func (codex) Read(req Request) (ReadResult, error) {
	source := req.Records
	if source == "" {
		resolved, err := ResolveSource("codex", "")
		if err != nil {
			return ReadResult{}, err
		}
		source = resolved
	}
	files, err := codexFiles(source)
	if err != nil {
		return ReadResult{}, err
	}
	if len(files) == 0 {
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "empty_source", Message: "record location contains no records", Scope: "source",
		}}}, nil
	}
	parsed := make([]codexFile, 0, len(files))
	for _, file := range files {
		parsed = append(parsed, parseCodexFile(source, file))
	}
	threads := indexCodexThreads(parsed)
	matches := threads.byPath[req.Agent]
	switch len(matches) {
	case 0:
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "agent_not_found", Message: "requested agent was not found", Scope: "agent", AgentID: req.Agent,
		}}}, nil
	case 1:
	default:
		return ReadResult{Diagnostics: []Diagnostic{{
			Code: "ambiguous_reference", Message: "the returned agent path matches multiple threads", Scope: "agent", AgentID: req.Agent,
		}}}, nil
	}
	target := matches[0]
	included := descendantThreads(target, threads)
	return buildCodexResult(target, included, threads, parsed), nil
}

func codexFiles(source string) ([]string, error) {
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
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
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

type codexFile struct {
	rel       string
	threadID  string
	records   []codexRecord
	truncated bool
}

type codexRecord struct {
	line     int
	time     time.Time
	kind     string
	threadID string
	unknown  bool
	meta     *codexSessionMeta
	task     *codexTask
	context  *codexTurnContext
	usage    *codexUsage
	item     *codexItem
}

type codexSessionMeta struct {
	ID, ParentThreadID, AgentPath, ModelProvider string
}

type codexTask struct {
	Type, TurnID, ParentTurnID, Reason string
}

type codexTurnContext struct {
	TurnID, Model string
}

type codexUsage struct {
	ThreadID, TurnID, ResponseID string
	Tokens                       codexCounters
}

type codexCounters struct {
	Input, CacheRead, CacheWrite, Output, Reasoning, Total *int64
}

type codexItem struct {
	TurnID, ThreadID, CallID, Kind, AgentThreadID, AgentPath string
}

type codexThread struct {
	id, path, provider, parent, file string
	models                           map[string][]string
}

type codexIndex struct {
	threads map[string]*codexThread
	byPath  map[string][]string
}

func parseCodexFile(root, path string) codexFile {
	file := codexFile{rel: codexRel(root, path)}
	handle, err := os.Open(path)
	if err != nil {
		file.truncated = true
		return file
	}
	defer handle.Close()
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		record, ok := parseCodexLine(lineNo, line)
		if !ok {
			file.truncated = true
			break
		}
		if record.meta != nil && file.threadID == "" {
			file.threadID = record.meta.ID
		}
		if record.threadID != "" && file.threadID == "" {
			file.threadID = record.threadID
		}
		file.records = append(file.records, record)
	}
	if err := scanner.Err(); err != nil {
		file.truncated = true
	}
	return file
}

func parseCodexLine(line int, raw string) (codexRecord, bool) {
	var envelope struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil || envelope.Type == "" {
		return codexRecord{}, false
	}
	record := codexRecord{line: line, kind: envelope.Type}
	if envelope.Timestamp != "" {
		record.time, _ = time.Parse(time.RFC3339Nano, envelope.Timestamp)
	}
	if !knownCodexEnvelope(envelope.Type) {
		record.unknown = true
		return record, true
	}
	if len(envelope.Payload) == 0 || string(envelope.Payload) == "null" {
		return record, true
	}
	switch envelope.Type {
	case "session_meta":
		var payload struct {
			ID             string `json:"id"`
			ParentThreadID string `json:"parent_thread_id"`
			AgentPath      string `json:"agent_path"`
			ModelProvider  string `json:"model_provider"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			record.unknown = true
			return record, true
		}
		record.meta = &codexSessionMeta{ID: payload.ID, ParentThreadID: payload.ParentThreadID, AgentPath: payload.AgentPath, ModelProvider: payload.ModelProvider}
		record.threadID = payload.ID
	case "turn_context":
		var payload struct {
			TurnID string `json:"turn_id"`
			Model  string `json:"model"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			record.unknown = true
			return record, true
		}
		record.context = &codexTurnContext{TurnID: payload.TurnID, Model: payload.Model}
	case "token_usage_record":
		var payload struct {
			ThreadID   string `json:"thread_id"`
			TurnID     string `json:"turn_id"`
			ResponseID string `json:"response_id"`
			Usage      struct {
				Input      *int64 `json:"input_tokens"`
				CacheRead  *int64 `json:"cached_input_tokens"`
				CacheWrite *int64 `json:"cache_write_input_tokens"`
				Output     *int64 `json:"output_tokens"`
				Reasoning  *int64 `json:"reasoning_output_tokens"`
				Total      *int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			record.unknown = true
			return record, true
		}
		record.usage = &codexUsage{
			ThreadID: payload.ThreadID, TurnID: payload.TurnID, ResponseID: payload.ResponseID,
			Tokens: codexCounters{payload.Usage.Input, payload.Usage.CacheRead, payload.Usage.CacheWrite, payload.Usage.Output, payload.Usage.Reasoning, payload.Usage.Total},
		}
		record.threadID = payload.ThreadID
	case "event_msg":
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(envelope.Payload, &kind); err != nil || !knownCodexEvent(kind.Type) {
			record.unknown = true
			return record, true
		}
		switch kind.Type {
		case "task_started", "task_complete", "turn_aborted":
			var payload struct {
				Type            string `json:"type"`
				TurnID          string `json:"turn_id"`
				Reason          string `json:"reason"`
				TurnAttribution struct {
					ParentTurnID string `json:"parent_turn_id"`
				} `json:"turn_attribution"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				record.unknown = true
				return record, true
			}
			record.task = &codexTask{Type: payload.Type, TurnID: payload.TurnID, ParentTurnID: payload.TurnAttribution.ParentTurnID, Reason: payload.Reason}
		case "item_completed", "item_started":
			var payload struct {
				TurnID   string `json:"turn_id"`
				ThreadID string `json:"thread_id"`
				Item     struct {
					Type          string `json:"type"`
					ID            string `json:"id"`
					Kind          string `json:"kind"`
					AgentThreadID string `json:"agent_thread_id"`
					AgentPath     string `json:"agent_path"`
				} `json:"item"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				record.unknown = true
				return record, true
			}
			if payload.Item.Type != "SubAgentActivity" {
				return record, true
			}
			record.item = &codexItem{
				TurnID: payload.TurnID, ThreadID: payload.ThreadID, CallID: payload.Item.ID,
				Kind: payload.Item.Kind, AgentThreadID: payload.Item.AgentThreadID, AgentPath: payload.Item.AgentPath,
			}
			record.threadID = payload.ThreadID
		}
	case "response_item":
		var payload struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || !knownCodexResponse(payload.Type) {
			record.unknown = true
			return record, true
		}
		if payload.Type == "function_call_output" && payload.Output != "" {
			var returned struct {
				TaskName string `json:"task_name"`
			}
			// Output is a JSON string. Only the returned task path is retained.
			if err := json.Unmarshal([]byte(payload.Output), &returned); err == nil && returned.TaskName != "" && record.item == nil {
				record.item = &codexItem{CallID: payload.CallID, AgentPath: returned.TaskName, Kind: "returned"}
			}
		}
	}
	return record, true
}

func knownCodexEnvelope(kind string) bool {
	switch kind {
	case "session_meta", "turn_context", "token_usage_record", "event_msg", "response_item", "inter_agent_communication_metadata":
		return true
	default:
		return false
	}
}

func knownCodexEvent(kind string) bool {
	switch kind {
	case "task_started", "task_complete", "turn_aborted", "token_count", "item_completed", "item_started":
		return true
	default:
		return false
	}
}

func knownCodexResponse(kind string) bool {
	switch kind {
	case "function_call", "function_call_output", "message", "reasoning":
		return true
	default:
		return false
	}
}

func indexCodexThreads(files []codexFile) codexIndex {
	index := codexIndex{threads: map[string]*codexThread{}, byPath: map[string][]string{}}
	addPath := func(path, threadID string) {
		if path == "" || threadID == "" {
			return
		}
		for _, existing := range index.byPath[path] {
			if existing == threadID {
				return
			}
		}
		index.byPath[path] = append(index.byPath[path], threadID)
	}
	ensure := func(id string) *codexThread {
		if id == "" {
			return nil
		}
		thread, ok := index.threads[id]
		if !ok {
			thread = &codexThread{id: id, models: map[string][]string{}}
			index.threads[id] = thread
		}
		return thread
	}
	for _, file := range files {
		for _, record := range file.records {
			if record.meta != nil {
				thread := ensure(record.meta.ID)
				if thread == nil {
					continue
				}
				thread.file = file.rel
				if record.meta.ParentThreadID != "" {
					thread.parent = record.meta.ParentThreadID
				}
				if record.meta.ModelProvider != "" {
					thread.provider = record.meta.ModelProvider
				}
				if record.meta.AgentPath != "" {
					thread.path = record.meta.AgentPath
					addPath(record.meta.AgentPath, thread.id)
				}
			}
			if record.context != nil && file.threadID != "" {
				thread := ensure(file.threadID)
				thread.models[record.context.TurnID] = append(thread.models[record.context.TurnID], record.context.Model)
			}
			if record.item != nil && record.item.AgentThreadID != "" {
				thread := ensure(record.item.AgentThreadID)
				if record.item.AgentPath != "" && thread.path == "" {
					thread.path = record.item.AgentPath
				}
				addPath(record.item.AgentPath, record.item.AgentThreadID)
			}
		}
	}
	for path := range index.byPath {
		sort.Strings(index.byPath[path])
	}
	return index
}

func descendantThreads(target string, index codexIndex) map[string]bool {
	included := map[string]bool{}
	if target != "" {
		included[target] = true
	}
	for changed := true; changed; {
		changed = false
		for id, thread := range index.threads {
			if included[id] || thread.parent == "" || !included[thread.parent] {
				continue
			}
			included[id] = true
			changed = true
		}
	}
	return included
}

func buildCodexResult(target string, included map[string]bool, index codexIndex, files []codexFile) ReadResult {
	result := ReadResult{AgentID: target}
	seenActivity := map[string]bool{}
	for _, file := range files {
		threadID := file.threadID
		if threadID == "" || !included[threadID] {
			continue
		}
		if file.truncated {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "truncated_record", Message: "a record file is truncated", Scope: "agent", AgentID: threadID, SourceID: file.rel,
			})
		}
		thread := index.threads[threadID]
		provider := ""
		if thread != nil {
			provider = thread.provider
		}
		if provider == "" {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "unknown_provider", Message: "session metadata has no model provider", Scope: "agent", AgentID: threadID, Metric: "provider",
			})
		}
		for _, record := range file.records {
			sourceID := file.rel + ":" + strconv.Itoa(record.line)
			if record.unknown {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{
					Code: "unsupported_record", Message: "an unsupported record type was ignored", Scope: "agent", AgentID: threadID, SourceID: sourceID,
				})
				continue
			}
			switch {
			case record.task != nil && record.task.TurnID != "":
				event := Event{
					ID: sourceID, Session: threadID, SourceSession: threadID, Execution: record.task.TurnID,
					ParentExecution: record.task.ParentTurnID, Time: record.time,
				}
				switch record.task.Type {
				case "task_started":
					event.Kind = "execution_start"
					event.Status = "running"
					event.StartDefinition = "task_started envelope timestamp"
				case "task_complete":
					event.Kind = "execution_end"
					event.Status = "completed"
					event.EndDefinition = "task_complete envelope timestamp"
				case "turn_aborted":
					event.Kind = "execution_end"
					event.Status = codexAbortStatus(record.task.Reason)
					event.EndDefinition = "turn_aborted envelope timestamp"
				}
				result.Events = append(result.Events, event)
			case record.usage != nil:
				model := ""
				if record.usage.TurnID != "" {
					if resolved, ok := codexModel(thread, record.usage.TurnID); ok {
						model = resolved
					}
				}
				usageThread := record.usage.ThreadID
				if usageThread == "" {
					usageThread = threadID
				}
				result.Events = append(result.Events, Event{
					ID: sourceID, UsageID: record.usage.ResponseID, Session: usageThread, SourceSession: usageThread,
					Execution: record.usage.TurnID, Kind: "usage", Provider: provider, Model: model,
					Input: record.usage.Tokens.Input, CacheRead: record.usage.Tokens.CacheRead, CacheWrite: record.usage.Tokens.CacheWrite,
					Output: record.usage.Tokens.Output, Reasoning: record.usage.Tokens.Reasoning, Total: record.usage.Tokens.Total,
				})
			case record.item != nil && record.item.AgentThreadID != "" && record.item.Kind != "returned":
				key := record.item.AgentThreadID + "|" + record.item.TurnID
				if seenActivity[key] {
					continue
				}
				seenActivity[key] = true
				if included[record.item.AgentThreadID] && index.threads[record.item.AgentThreadID] != nil && index.threads[record.item.AgentThreadID].file != "" {
					continue
				}
				child := record.item.AgentPath
				if child == "" {
					child = record.item.AgentThreadID
				}
				result.Delegations = append(result.Delegations, Delegation{
					ParentAgentID: threadID,
					ChildAgentID:  child,
					SourceID:      sourceID,
					Parent:        &ExecutionRef{AgentID: threadID, ExecutionID: record.item.TurnID},
				})
			}
		}
	}
	return result
}

func codexModel(thread *codexThread, turnID string) (string, bool) {
	if thread == nil {
		return "", false
	}
	models := thread.models[turnID]
	if len(models) == 0 {
		return "", false
	}
	first := models[0]
	for _, model := range models {
		if model == "" || model != first {
			return "", false
		}
	}
	return first, first != ""
}

func codexAbortStatus(reason string) string {
	switch reason {
	case "interrupted", "failed":
		return reason
	default:
		return "interrupted"
	}
}

func codexRel(root, path string) string {
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
