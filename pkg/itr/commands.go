// Package itr defines the binary command protocol for the PicoClaw Isolated
// Tool Runtime (ITR). All tool invocations — traditional tools and RLM
// recursive decomposition operations — are serialized as ToolRequest /
// ToolResponse pairs.
//
// The canonical schema lives in commands.fbs. Wire encoding uses FlatBuffers
// for all internal transport (socket, daemon, WASM). JSON encoding is
// available via MarshalJSON / UnmarshalRequestJSON for the LLM boundary only.
package itr

import (
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"time"
)

// CommandType identifies which command variant is carried in a ToolRequest.
type CommandType string

const (
	// RLM decomposition operations
	CmdPeek      CommandType = "peek"
	CmdGrep      CommandType = "grep"
	CmdPartition CommandType = "partition"
	CmdRecurse   CommandType = "recurse"

	// Traditional tool execution
	CmdToolExec CommandType = "tool_exec"
	CmdExecWasm CommandType = "exec_wasm"

	// Terminal answer from an RLM recursion
	CmdFinal CommandType = "final"

	// On-demand tool discovery (mirrors Anthropic Tool Search)
	CmdToolSearch CommandType = "tool_search"

	// Sandboxed code execution (wazero-isolated)
	CmdCodeExec CommandType = "code_exec"

	// DAG plan: a composite payload containing multiple nodes with dependencies
	CmdDAGPlan CommandType = "dag_plan"
)

// ── Payload types ────────────────────────────────────────────────────────────

// Peek reads a byte range from the context rope.
type Peek struct {
	Start  uint64 `json:"start"`            // byte offset into context
	Length uint32 `json:"length,omitempty"` // 0 = read to end
}

// Grep searches the context for a pattern.
type Grep struct {
	Pattern         string `json:"pattern"`
	MaxMatches      uint32 `json:"max_matches,omitempty"`      // 0 → 50
	CaseInsensitive bool   `json:"case_insensitive,omitempty"` // default false
}

// Partition splits the context into k chunks.
type Partition struct {
	K        uint32 `json:"k,omitempty"`        // 0 → 4
	Method   string `json:"method,omitempty"`   // "uniform" | "semantic"
	Overlap  uint32 `json:"overlap,omitempty"`  // token overlap between chunks
	Semantic bool   `json:"semantic,omitempty"` // use sentence/paragraph boundaries
}

// Recurse spawns a sub-RLM call over a context partition.
type Recurse struct {
	SubQuery   string `json:"sub_query"`
	ContextKey string `json:"context_key"`
	DepthHint  uint8  `json:"depth_hint,omitempty"` // 0 → 3
}

// ToolExec invokes a named tool with JSON-serialized arguments.
type ToolExec struct {
	ToolName string `json:"tool_name"`
	ArgsJSON string `json:"args_json"` // JSON object
}

// ExecWasm runs a function in a wazero WASM isolate (Layer 5).
type ExecWasm struct {
	ModuleKey string `json:"module_key"`
	Entry     string `json:"entry"`
	InputJSON string `json:"input_json,omitempty"`
}

// Final records the terminal answer from an RLM recursion.
type Final struct {
	Answer  string `json:"answer"`
	VarName string `json:"var_name,omitempty"`
}

// ToolSearch discovers tools matching a query. The executor returns a list
// of ToolInfo entries the DAG planner can reference in subsequent ToolExec nodes.
type ToolSearch struct {
	Query      string `json:"query"`
	MaxResults uint8  `json:"max_results,omitempty"` // 0 → 10
}

// CodeExec runs code in a wazero-isolated sandbox (opt-in only, Layer 5).
type CodeExec struct {
	Code     string `json:"code"`
	Language string `json:"language"` // e.g. "javascript", "python-wasm"
}

// DAGNode is a single unit of work within a DAGPlan. Each node carries a
// command payload and declares dependencies on other nodes by ID.
// Args may contain "#nodeN" references that are resolved to the output of
// the dependency node at execution time.
//
// Implements json.Unmarshaler to deserialize Payload directly into the
// concrete type indicated by Type, avoiding the map[string]interface{}
// erasure that encoding/json applies to interface{} fields.
type DAGNode struct {
	ID        string      `json:"id"`
	Type      CommandType `json:"type"`
	Payload   interface{} `json:"payload"`
	DependsOn []string    `json:"depends_on,omitempty"`
}

// UnmarshalJSONFrom implements jsonv2.UnmarshalerFrom. It defers Payload
// deserialization via jsontext.Value until the Type discriminator is known,
// then unmarshals directly into the concrete Go type.
func (n *DAGNode) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var raw struct {
		ID        string         `json:"id"`
		Type      CommandType    `json:"type"`
		Payload   jsontext.Value `json:"payload"`
		DependsOn []string       `json:"depends_on,omitempty"`
	}
	if err := jsonv2.UnmarshalDecode(dec, &raw); err != nil {
		return err
	}
	n.ID = raw.ID
	n.Type = raw.Type
	n.DependsOn = raw.DependsOn

	if len(raw.Payload) == 0 || string(raw.Payload) == "null" {
		return nil
	}

	unmarshalPayload := func(dst any) error {
		return jsonv2.Unmarshal(raw.Payload, dst, jsonv2.MatchCaseInsensitiveNames(true))
	}

	switch raw.Type {
	case CmdToolExec:
		var p ToolExec
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdToolSearch:
		var p ToolSearch
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdPeek:
		var p Peek
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdGrep:
		var p Grep
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdPartition:
		var p Partition
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdRecurse:
		var p Recurse
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdCodeExec:
		var p CodeExec
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdExecWasm:
		var p ExecWasm
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	case CmdFinal:
		var p Final
		if err := unmarshalPayload(&p); err != nil {
			return fmt.Errorf("DAGNode %s payload: %w", raw.ID, err)
		}
		n.Payload = p
	default:
		return fmt.Errorf("DAGNode %s: unknown type %q", raw.ID, raw.Type)
	}
	return nil
}

// DAGPlan is a composite command that contains multiple DAGNodes forming a
// Directed Acyclic Graph. The executor dispatches nodes in topological order,
// running independent nodes in parallel.
type DAGPlan struct {
	Nodes       []DAGNode `json:"nodes"`
	MaxParallel uint8     `json:"max_parallel,omitempty"` // 0 → GOMAXPROCS
	TokenBudget uint32    `json:"token_budget,omitempty"`
	JoinerQuery string    `json:"joiner_query,omitempty"` // synthesis prompt
}

// ── Envelope ─────────────────────────────────────────────────────────────────

// ToolRequest is the envelope transmitted from the agent loop to the SecureBus.
// Exactly one of the command payload fields should be non-nil.
type ToolRequest struct {
	ID         string      `json:"id"`                     // UUIDv7
	Type       CommandType `json:"type"`                   // discriminator
	Payload    interface{} `json:"payload"`                // one of the Cmd* types
	Timestamp  int64       `json:"timestamp"`              // Unix nanoseconds
	Depth      uint8       `json:"depth,omitempty"`        // recursion depth
	SessionKey string      `json:"session_key,omitempty"`  // conversation scope
	ToolCallID string      `json:"tool_call_id,omitempty"` // LLM tool_call.id
}

// ToolResponse is returned from the SecureBus to the agent loop.
type ToolResponse struct {
	ID           string   `json:"id"`               // mirrors request ID
	Result       string   `json:"result,omitempty"` // JSON-serialised result
	IsError      bool     `json:"is_error,omitempty"`
	LeakDetected bool     `json:"leak_detected,omitempty"` // Redactor triggered
	CostTokens   uint32   `json:"cost_tokens,omitempty"`   // tokens consumed
	RedactedKeys []string `json:"redacted_keys,omitempty"` // keys that were redacted
}

// ── Constructors ─────────────────────────────────────────────────────────────

func NewToolExecRequest(id, sessionKey, toolCallID, toolName, argsJSON string) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdToolExec,
		Payload:    ToolExec{ToolName: toolName, ArgsJSON: argsJSON},
		Timestamp:  time.Now().UnixNano(),
		SessionKey: sessionKey,
		ToolCallID: toolCallID,
	}
}

func NewPeekRequest(id, sessionKey string, depth uint8, start uint64, length uint32) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdPeek,
		Payload:    Peek{Start: start, Length: length},
		Timestamp:  time.Now().UnixNano(),
		Depth:      depth,
		SessionKey: sessionKey,
	}
}

func NewGrepRequest(id, sessionKey string, depth uint8, pattern string, maxMatches uint32, caseInsensitive bool) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdGrep,
		Payload:    Grep{Pattern: pattern, MaxMatches: maxMatches, CaseInsensitive: caseInsensitive},
		Timestamp:  time.Now().UnixNano(),
		Depth:      depth,
		SessionKey: sessionKey,
	}
}

func NewPartitionRequest(id, sessionKey string, depth uint8, k uint32, method string, overlap uint32, semantic bool) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdPartition,
		Payload:    Partition{K: k, Method: method, Overlap: overlap, Semantic: semantic},
		Timestamp:  time.Now().UnixNano(),
		Depth:      depth,
		SessionKey: sessionKey,
	}
}

func NewRecurseRequest(id, sessionKey string, depth uint8, subQuery, contextKey string, depthHint uint8) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdRecurse,
		Payload:    Recurse{SubQuery: subQuery, ContextKey: contextKey, DepthHint: depthHint},
		Timestamp:  time.Now().UnixNano(),
		Depth:      depth,
		SessionKey: sessionKey,
	}
}

func NewFinalRequest(id, sessionKey string, depth uint8, answer, varName string) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdFinal,
		Payload:    Final{Answer: answer, VarName: varName},
		Timestamp:  time.Now().UnixNano(),
		Depth:      depth,
		SessionKey: sessionKey,
	}
}

func NewToolSearchRequest(id, sessionKey string, query string, maxResults uint8) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdToolSearch,
		Payload:    ToolSearch{Query: query, MaxResults: maxResults},
		Timestamp:  time.Now().UnixNano(),
		SessionKey: sessionKey,
	}
}

func NewCodeExecRequest(id, sessionKey string, code, language string) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdCodeExec,
		Payload:    CodeExec{Code: code, Language: language},
		Timestamp:  time.Now().UnixNano(),
		SessionKey: sessionKey,
	}
}

func NewDAGPlanRequest(id, sessionKey string, plan DAGPlan) ToolRequest {
	return ToolRequest{
		ID:         id,
		Type:       CmdDAGPlan,
		Payload:    plan,
		Timestamp:  time.Now().UnixNano(),
		SessionKey: sessionKey,
	}
}

func NewSuccessResponse(id, result string, costTokens uint32) ToolResponse {
	return ToolResponse{ID: id, Result: result, CostTokens: costTokens}
}

func NewErrorResponse(id, errMsg string) ToolResponse {
	return ToolResponse{ID: id, Result: errMsg, IsError: true}
}

func NewLeakResponse(id, redactedResult string, redactedKeys []string) ToolResponse {
	return ToolResponse{
		ID:           id,
		Result:       redactedResult,
		LeakDetected: true,
		RedactedKeys: redactedKeys,
	}
}

// ── Serialisation helpers (FlatBuffers default) ──────────────────────────────

// Marshal encodes a ToolRequest to FlatBuffers bytes for internal transport.
func (r ToolRequest) Marshal() ([]byte, error) {
	return MarshalRequestFB(r)
}

// Marshal encodes a ToolResponse to FlatBuffers bytes for internal transport.
func (r ToolResponse) Marshal() ([]byte, error) {
	return MarshalResponseFB(r)
}

// UnmarshalRequest decodes a ToolRequest from FlatBuffers bytes.
func UnmarshalRequest(data []byte) (ToolRequest, error) {
	return UnmarshalRequestFB(data)
}

// UnmarshalResponse decodes a ToolResponse from FlatBuffers bytes.
func UnmarshalResponse(data []byte) (ToolResponse, error) {
	return UnmarshalResponseFB(data)
}

// ── JSON helpers (LLM boundary only) ─────────────────────────────────────────

// MarshalJSONTo implements jsonv2.MarshalerTo. Use only at the LLM boundary.
func (r ToolRequest) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias struct {
		ID         string      `json:"id"`
		Type       CommandType `json:"type"`
		Payload    interface{} `json:"payload"`
		Timestamp  int64       `json:"timestamp"`
		Depth      uint8       `json:"depth,omitzero"`
		SessionKey string      `json:"session_key,omitzero"`
		ToolCallID string      `json:"tool_call_id,omitzero"`
	}
	return jsonv2.MarshalEncode(enc, alias{r.ID, r.Type, r.Payload, r.Timestamp, r.Depth, r.SessionKey, r.ToolCallID})
}

// MarshalJSONTo implements jsonv2.MarshalerTo. Use only at the LLM boundary.
func (r ToolResponse) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias struct {
		ID           string   `json:"id"`
		Result       string   `json:"result,omitzero"`
		IsError      bool     `json:"is_error,omitzero"`
		LeakDetected bool     `json:"leak_detected,omitzero"`
		CostTokens   uint32   `json:"cost_tokens,omitzero"`
		RedactedKeys []string `json:"redacted_keys,omitzero"`
	}
	return jsonv2.MarshalEncode(enc, alias{r.ID, r.Result, r.IsError, r.LeakDetected, r.CostTokens, r.RedactedKeys})
}

// LLMJSONOpts returns json/v2 options suitable for unmarshaling LLM-generated JSON,
// which may use inconsistent casing.
func LLMJSONOpts() jsonv2.Options {
	return jsonv2.MatchCaseInsensitiveNames(true)
}

// UnmarshalRequestJSON decodes a ToolRequest from JSON bytes. Use only at
// the LLM boundary (planner output, tool schemas).
func UnmarshalRequestJSON(data []byte) (ToolRequest, error) {
	opts := LLMJSONOpts()
	var raw struct {
		ID         string         `json:"id"`
		Type       CommandType    `json:"type"`
		Payload    jsontext.Value `json:"payload"`
		Timestamp  int64          `json:"timestamp"`
		Depth      uint8          `json:"depth"`
		SessionKey string         `json:"session_key"`
		ToolCallID string         `json:"tool_call_id"`
	}
	if err := jsonv2.Unmarshal(data, &raw, opts); err != nil {
		return ToolRequest{}, err
	}

	req := ToolRequest{
		ID:         raw.ID,
		Type:       raw.Type,
		Timestamp:  raw.Timestamp,
		Depth:      raw.Depth,
		SessionKey: raw.SessionKey,
		ToolCallID: raw.ToolCallID,
	}

	unmarshalPayload := func(dst any) error {
		return jsonv2.Unmarshal(raw.Payload, dst, opts)
	}

	var err error
	switch raw.Type {
	case CmdPeek:
		var p Peek
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdGrep:
		var p Grep
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdPartition:
		var p Partition
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdRecurse:
		var p Recurse
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdToolExec:
		var p ToolExec
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdExecWasm:
		var p ExecWasm
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdFinal:
		var p Final
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdToolSearch:
		var p ToolSearch
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdCodeExec:
		var p CodeExec
		err = unmarshalPayload(&p)
		req.Payload = p
	case CmdDAGPlan:
		var p DAGPlan
		err = unmarshalPayload(&p)
		req.Payload = p
	default:
		return ToolRequest{}, fmt.Errorf("unknown command type: %q", raw.Type)
	}

	return req, err
}

// UnmarshalResponseJSON decodes a ToolResponse from JSON bytes.
func UnmarshalResponseJSON(data []byte) (ToolResponse, error) {
	var r ToolResponse
	return r, jsonv2.Unmarshal(data, &r)
}
