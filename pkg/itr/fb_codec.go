package itr

import (
	"fmt"

	flatbuffers "github.com/google/flatbuffers/go"
	"github.com/sipeed/picoclaw/pkg/itr/itrfb"
)

// ── Domain ↔ FlatBuffers enum mapping ───────────────────────────────────────

var cmdTypeToFB = map[CommandType]itrfb.CmdType{
	CmdPeek:       itrfb.CmdTypePeek,
	CmdGrep:       itrfb.CmdTypeGrep,
	CmdPartition:  itrfb.CmdTypePartition,
	CmdRecurse:    itrfb.CmdTypeRecurse,
	CmdToolExec:   itrfb.CmdTypeToolExec,
	CmdExecWasm:   itrfb.CmdTypeExecWasm,
	CmdFinal:      itrfb.CmdTypeFinal,
	CmdToolSearch: itrfb.CmdTypeToolSearch,
	CmdCodeExec:   itrfb.CmdTypeCodeExec,
	CmdDAGPlan:    itrfb.CmdTypeDAGPlan,
}

var fbToCmdType = map[itrfb.CmdType]CommandType{
	itrfb.CmdTypePeek:       CmdPeek,
	itrfb.CmdTypeGrep:       CmdGrep,
	itrfb.CmdTypePartition:  CmdPartition,
	itrfb.CmdTypeRecurse:    CmdRecurse,
	itrfb.CmdTypeToolExec:   CmdToolExec,
	itrfb.CmdTypeExecWasm:   CmdExecWasm,
	itrfb.CmdTypeFinal:      CmdFinal,
	itrfb.CmdTypeToolSearch: CmdToolSearch,
	itrfb.CmdTypeCodeExec:   CmdCodeExec,
	itrfb.CmdTypeDAGPlan:    CmdDAGPlan,
}

// ── MarshalRequestFB ────────────────────────────────────────────────────────

func MarshalRequestFB(r ToolRequest) ([]byte, error) {
	b := flatbuffers.NewBuilder(512)

	idOff := b.CreateString(r.ID)
	skOff := b.CreateString(r.SessionKey)
	tcOff := b.CreateString(r.ToolCallID)

	fbCmdType, ok := cmdTypeToFB[r.Type]
	if !ok {
		return nil, fmt.Errorf("unknown command type: %q", r.Type)
	}

	if r.Type == CmdDAGPlan {
		plan, ok := r.Payload.(DAGPlan)
		if !ok {
			return nil, fmt.Errorf("payload is not DAGPlan")
		}
		dagOff, err := buildDAGPlan(b, &plan)
		if err != nil {
			return nil, err
		}
		itrfb.ToolRequestStart(b)
		itrfb.ToolRequestAddId(b, idOff)
		itrfb.ToolRequestAddCmdType(b, fbCmdType)
		itrfb.ToolRequestAddDagPlan(b, dagOff)
		itrfb.ToolRequestAddTimestamp(b, uint64(r.Timestamp))
		itrfb.ToolRequestAddDepth(b, r.Depth)
		itrfb.ToolRequestAddSessionKey(b, skOff)
		itrfb.ToolRequestAddToolCallId(b, tcOff)
		reqOff := itrfb.ToolRequestEnd(b)
		itrfb.FinishToolRequestBuffer(b, reqOff)
		return b.FinishedBytes(), nil
	}

	payloadType, payloadOff, err := buildPayload(b, r.Type, r.Payload)
	if err != nil {
		return nil, err
	}

	itrfb.ToolRequestStart(b)
	itrfb.ToolRequestAddId(b, idOff)
	itrfb.ToolRequestAddCmdType(b, fbCmdType)
	itrfb.ToolRequestAddPayloadType(b, payloadType)
	itrfb.ToolRequestAddPayload(b, payloadOff)
	itrfb.ToolRequestAddTimestamp(b, uint64(r.Timestamp))
	itrfb.ToolRequestAddDepth(b, r.Depth)
	itrfb.ToolRequestAddSessionKey(b, skOff)
	itrfb.ToolRequestAddToolCallId(b, tcOff)
	reqOff := itrfb.ToolRequestEnd(b)
	itrfb.FinishToolRequestBuffer(b, reqOff)
	return b.FinishedBytes(), nil
}

// ── UnmarshalRequestFB ──────────────────────────────────────────────────────

func UnmarshalRequestFB(data []byte) (ToolRequest, error) {
	fb := itrfb.GetRootAsToolRequest(data, 0)

	ct, ok := fbToCmdType[fb.CmdType()]
	if !ok {
		return ToolRequest{}, fmt.Errorf("unknown FlatBuffers CmdType: %d", fb.CmdType())
	}

	req := ToolRequest{
		ID:         string(fb.Id()),
		Type:       ct,
		Timestamp:  int64(fb.Timestamp()),
		Depth:      fb.Depth(),
		SessionKey: string(fb.SessionKey()),
		ToolCallID: string(fb.ToolCallId()),
	}

	if ct == CmdDAGPlan {
		fbPlan := fb.DagPlan(nil)
		if fbPlan == nil {
			return ToolRequest{}, fmt.Errorf("DAGPlan request missing dag_plan field")
		}
		plan, err := readDAGPlan(fbPlan)
		if err != nil {
			return ToolRequest{}, err
		}
		req.Payload = plan
		return req, nil
	}

	payload, err := readPayload(fb.PayloadType(), fb)
	if err != nil {
		return ToolRequest{}, err
	}
	req.Payload = payload
	return req, nil
}

// ── MarshalResponseFB ───────────────────────────────────────────────────────

func MarshalResponseFB(r ToolResponse) ([]byte, error) {
	b := flatbuffers.NewBuilder(256)

	idOff := b.CreateString(r.ID)
	resultOff := b.CreateString(r.Result)

	var keysOff flatbuffers.UOffsetT
	if len(r.RedactedKeys) > 0 {
		keyOffsets := make([]flatbuffers.UOffsetT, len(r.RedactedKeys))
		for i := len(r.RedactedKeys) - 1; i >= 0; i-- {
			keyOffsets[i] = b.CreateString(r.RedactedKeys[i])
		}
		itrfb.ToolResponseStartRedactedKeysVector(b, len(r.RedactedKeys))
		for i := len(keyOffsets) - 1; i >= 0; i-- {
			b.PrependUOffsetT(keyOffsets[i])
		}
		keysOff = b.EndVector(len(r.RedactedKeys))
	}

	itrfb.ToolResponseStart(b)
	itrfb.ToolResponseAddId(b, idOff)
	itrfb.ToolResponseAddResult(b, resultOff)
	itrfb.ToolResponseAddIsError(b, r.IsError)
	itrfb.ToolResponseAddLeakDetected(b, r.LeakDetected)
	itrfb.ToolResponseAddCostTokens(b, r.CostTokens)
	if len(r.RedactedKeys) > 0 {
		itrfb.ToolResponseAddRedactedKeys(b, keysOff)
	}
	respOff := itrfb.ToolResponseEnd(b)
	itrfb.FinishToolResponseBuffer(b, respOff)
	return b.FinishedBytes(), nil
}

// ── UnmarshalResponseFB ─────────────────────────────────────────────────────

func UnmarshalResponseFB(data []byte) (ToolResponse, error) {
	fb := itrfb.GetRootAsToolResponse(data, 0)

	r := ToolResponse{
		ID:           string(fb.Id()),
		Result:       string(fb.Result()),
		IsError:      fb.IsError(),
		LeakDetected: fb.LeakDetected(),
		CostTokens:   fb.CostTokens(),
	}

	n := fb.RedactedKeysLength()
	if n > 0 {
		r.RedactedKeys = make([]string, n)
		for i := 0; i < n; i++ {
			r.RedactedKeys[i] = string(fb.RedactedKeys(i))
		}
	}
	return r, nil
}

// ── Payload builders ────────────────────────────────────────────────────────

func buildPayload(b *flatbuffers.Builder, ct CommandType, payload interface{}) (itrfb.CommandPayload, flatbuffers.UOffsetT, error) {
	switch ct {
	case CmdPeek:
		p, ok := payload.(Peek)
		if !ok {
			return 0, 0, fmt.Errorf("expected Peek payload, got %T", payload)
		}
		itrfb.PeekStart(b)
		itrfb.PeekAddStart(b, p.Start)
		itrfb.PeekAddLength(b, p.Length)
		return itrfb.CommandPayloadPeek, itrfb.PeekEnd(b), nil

	case CmdGrep:
		p, ok := payload.(Grep)
		if !ok {
			return 0, 0, fmt.Errorf("expected Grep payload, got %T", payload)
		}
		pat := b.CreateString(p.Pattern)
		itrfb.GrepStart(b)
		itrfb.GrepAddPattern(b, pat)
		itrfb.GrepAddMaxMatches(b, p.MaxMatches)
		itrfb.GrepAddCaseInsensitive(b, p.CaseInsensitive)
		return itrfb.CommandPayloadGrep, itrfb.GrepEnd(b), nil

	case CmdPartition:
		p, ok := payload.(Partition)
		if !ok {
			return 0, 0, fmt.Errorf("expected Partition payload, got %T", payload)
		}
		method := b.CreateString(p.Method)
		itrfb.PartitionStart(b)
		itrfb.PartitionAddK(b, p.K)
		itrfb.PartitionAddMethod(b, method)
		itrfb.PartitionAddOverlap(b, p.Overlap)
		itrfb.PartitionAddSemantic(b, p.Semantic)
		return itrfb.CommandPayloadPartition, itrfb.PartitionEnd(b), nil

	case CmdRecurse:
		p, ok := payload.(Recurse)
		if !ok {
			return 0, 0, fmt.Errorf("expected Recurse payload, got %T", payload)
		}
		sq := b.CreateString(p.SubQuery)
		ck := b.CreateString(p.ContextKey)
		itrfb.RecurseStart(b)
		itrfb.RecurseAddSubQuery(b, sq)
		itrfb.RecurseAddContextKey(b, ck)
		itrfb.RecurseAddDepthHint(b, p.DepthHint)
		return itrfb.CommandPayloadRecurse, itrfb.RecurseEnd(b), nil

	case CmdToolExec:
		p, ok := payload.(ToolExec)
		if !ok {
			return 0, 0, fmt.Errorf("expected ToolExec payload, got %T", payload)
		}
		tn := b.CreateString(p.ToolName)
		aj := b.CreateString(p.ArgsJSON)
		itrfb.ToolExecStart(b)
		itrfb.ToolExecAddToolName(b, tn)
		itrfb.ToolExecAddArgsJson(b, aj)
		return itrfb.CommandPayloadToolExec, itrfb.ToolExecEnd(b), nil

	case CmdExecWasm:
		p, ok := payload.(ExecWasm)
		if !ok {
			return 0, 0, fmt.Errorf("expected ExecWasm payload, got %T", payload)
		}
		mk := b.CreateString(p.ModuleKey)
		en := b.CreateString(p.Entry)
		ij := b.CreateString(p.InputJSON)
		itrfb.ExecWasmStart(b)
		itrfb.ExecWasmAddModuleKey(b, mk)
		itrfb.ExecWasmAddEntry(b, en)
		itrfb.ExecWasmAddInputJson(b, ij)
		return itrfb.CommandPayloadExecWasm, itrfb.ExecWasmEnd(b), nil

	case CmdFinal:
		p, ok := payload.(Final)
		if !ok {
			return 0, 0, fmt.Errorf("expected Final payload, got %T", payload)
		}
		ans := b.CreateString(p.Answer)
		vn := b.CreateString(p.VarName)
		itrfb.FinalStart(b)
		itrfb.FinalAddAnswer(b, ans)
		itrfb.FinalAddVarName(b, vn)
		return itrfb.CommandPayloadFinal, itrfb.FinalEnd(b), nil

	case CmdToolSearch:
		p, ok := payload.(ToolSearch)
		if !ok {
			return 0, 0, fmt.Errorf("expected ToolSearch payload, got %T", payload)
		}
		q := b.CreateString(p.Query)
		itrfb.ToolSearchStart(b)
		itrfb.ToolSearchAddQuery(b, q)
		itrfb.ToolSearchAddMaxResults(b, p.MaxResults)
		return itrfb.CommandPayloadToolSearch, itrfb.ToolSearchEnd(b), nil

	case CmdCodeExec:
		p, ok := payload.(CodeExec)
		if !ok {
			return 0, 0, fmt.Errorf("expected CodeExec payload, got %T", payload)
		}
		lang := b.CreateString(p.Language)
		code := b.CreateString(p.Code)
		itrfb.CodeExecStart(b)
		itrfb.CodeExecAddLanguage(b, lang)
		itrfb.CodeExecAddCode(b, code)
		return itrfb.CommandPayloadCodeExec, itrfb.CodeExecEnd(b), nil

	default:
		return 0, 0, fmt.Errorf("cannot build FlatBuffers payload for type %q", ct)
	}
}

// ── Payload readers ─────────────────────────────────────────────────────────

func readPayload(pt itrfb.CommandPayload, fb *itrfb.ToolRequest) (interface{}, error) {
	var tbl flatbuffers.Table
	if !fb.Payload(&tbl) {
		return nil, fmt.Errorf("missing payload in FlatBuffers ToolRequest")
	}

	switch pt {
	case itrfb.CommandPayloadPeek:
		var p itrfb.Peek
		p.Init(tbl.Bytes, tbl.Pos)
		return Peek{Start: p.Start(), Length: p.Length()}, nil

	case itrfb.CommandPayloadGrep:
		var p itrfb.Grep
		p.Init(tbl.Bytes, tbl.Pos)
		return Grep{
			Pattern:         string(p.Pattern()),
			MaxMatches:      p.MaxMatches(),
			CaseInsensitive: p.CaseInsensitive(),
		}, nil

	case itrfb.CommandPayloadPartition:
		var p itrfb.Partition
		p.Init(tbl.Bytes, tbl.Pos)
		return Partition{
			K:        p.K(),
			Method:   string(p.Method()),
			Overlap:  p.Overlap(),
			Semantic: p.Semantic(),
		}, nil

	case itrfb.CommandPayloadRecurse:
		var p itrfb.Recurse
		p.Init(tbl.Bytes, tbl.Pos)
		return Recurse{
			SubQuery:   string(p.SubQuery()),
			ContextKey: string(p.ContextKey()),
			DepthHint:  p.DepthHint(),
		}, nil

	case itrfb.CommandPayloadToolExec:
		var p itrfb.ToolExec
		p.Init(tbl.Bytes, tbl.Pos)
		return ToolExec{
			ToolName: string(p.ToolName()),
			ArgsJSON: string(p.ArgsJson()),
		}, nil

	case itrfb.CommandPayloadExecWasm:
		var p itrfb.ExecWasm
		p.Init(tbl.Bytes, tbl.Pos)
		return ExecWasm{
			ModuleKey: string(p.ModuleKey()),
			Entry:     string(p.Entry()),
			InputJSON: string(p.InputJson()),
		}, nil

	case itrfb.CommandPayloadFinal:
		var p itrfb.Final
		p.Init(tbl.Bytes, tbl.Pos)
		return Final{
			Answer:  string(p.Answer()),
			VarName: string(p.VarName()),
		}, nil

	case itrfb.CommandPayloadToolSearch:
		var p itrfb.ToolSearch
		p.Init(tbl.Bytes, tbl.Pos)
		return ToolSearch{
			Query:      string(p.Query()),
			MaxResults: p.MaxResults(),
		}, nil

	case itrfb.CommandPayloadCodeExec:
		var p itrfb.CodeExec
		p.Init(tbl.Bytes, tbl.Pos)
		return CodeExec{
			Language: string(p.Language()),
			Code:     string(p.Code()),
		}, nil

	case itrfb.CommandPayloadNONE:
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown FlatBuffers payload type: %d", pt)
	}
}

// ── DAGPlan builder/reader ──────────────────────────────────────────────────

func buildDAGPlan(b *flatbuffers.Builder, plan *DAGPlan) (flatbuffers.UOffsetT, error) {
	nodeOffsets := make([]flatbuffers.UOffsetT, len(plan.Nodes))
	for i := len(plan.Nodes) - 1; i >= 0; i-- {
		off, err := buildDAGNode(b, &plan.Nodes[i])
		if err != nil {
			return 0, fmt.Errorf("node %q: %w", plan.Nodes[i].ID, err)
		}
		nodeOffsets[i] = off
	}

	itrfb.DAGPlanStartNodesVector(b, len(nodeOffsets))
	for i := len(nodeOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(nodeOffsets[i])
	}
	nodesVec := b.EndVector(len(nodeOffsets))

	jq := b.CreateString(plan.JoinerQuery)

	itrfb.DAGPlanStart(b)
	itrfb.DAGPlanAddNodes(b, nodesVec)
	itrfb.DAGPlanAddMaxParallel(b, plan.MaxParallel)
	itrfb.DAGPlanAddTokenBudget(b, plan.TokenBudget)
	itrfb.DAGPlanAddJoinerQuery(b, jq)
	return itrfb.DAGPlanEnd(b), nil
}

func buildDAGNode(b *flatbuffers.Builder, node *DAGNode) (flatbuffers.UOffsetT, error) {
	idOff := b.CreateString(node.ID)

	depOffsets := make([]flatbuffers.UOffsetT, len(node.DependsOn))
	for i := len(node.DependsOn) - 1; i >= 0; i-- {
		depOffsets[i] = b.CreateString(node.DependsOn[i])
	}
	var depsVec flatbuffers.UOffsetT
	if len(depOffsets) > 0 {
		itrfb.DAGNodeStartDependsOnVector(b, len(depOffsets))
		for i := len(depOffsets) - 1; i >= 0; i-- {
			b.PrependUOffsetT(depOffsets[i])
		}
		depsVec = b.EndVector(len(depOffsets))
	}

	fbNodeType, ok := cmdTypeToFB[node.Type]
	if !ok {
		return 0, fmt.Errorf("unknown node type: %q", node.Type)
	}

	payloadType, payloadOff, err := buildPayload(b, node.Type, node.Payload)
	if err != nil {
		return 0, err
	}

	itrfb.DAGNodeStart(b)
	itrfb.DAGNodeAddId(b, idOff)
	itrfb.DAGNodeAddNodeType(b, fbNodeType)
	itrfb.DAGNodeAddPayloadType(b, payloadType)
	itrfb.DAGNodeAddPayload(b, payloadOff)
	if len(depOffsets) > 0 {
		itrfb.DAGNodeAddDependsOn(b, depsVec)
	}
	return itrfb.DAGNodeEnd(b), nil
}

func readDAGPlan(fb *itrfb.DAGPlan) (DAGPlan, error) {
	plan := DAGPlan{
		MaxParallel: fb.MaxParallel(),
		TokenBudget: fb.TokenBudget(),
		JoinerQuery: string(fb.JoinerQuery()),
	}

	n := fb.NodesLength()
	if n > 0 {
		plan.Nodes = make([]DAGNode, n)
		for i := 0; i < n; i++ {
			var fbNode itrfb.DAGNode
			if !fb.Nodes(&fbNode, i) {
				return DAGPlan{}, fmt.Errorf("failed to read DAG node at index %d", i)
			}
			node, err := readDAGNode(&fbNode)
			if err != nil {
				return DAGPlan{}, fmt.Errorf("node %d: %w", i, err)
			}
			plan.Nodes[i] = node
		}
	}
	return plan, nil
}

func readDAGNode(fb *itrfb.DAGNode) (DAGNode, error) {
	ct, ok := fbToCmdType[itrfb.CmdType(fb.NodeType())]
	if !ok {
		return DAGNode{}, fmt.Errorf("unknown FlatBuffers node type: %d", fb.NodeType())
	}

	node := DAGNode{
		ID:   string(fb.Id()),
		Type: ct,
	}

	ndeps := fb.DependsOnLength()
	if ndeps > 0 {
		node.DependsOn = make([]string, ndeps)
		for i := 0; i < ndeps; i++ {
			node.DependsOn[i] = string(fb.DependsOn(i))
		}
	}

	payload, err := readNodePayload(fb.PayloadType(), fb)
	if err != nil {
		return DAGNode{}, err
	}
	node.Payload = payload
	return node, nil
}

func readNodePayload(pt itrfb.CommandPayload, fb *itrfb.DAGNode) (interface{}, error) {
	var tbl flatbuffers.Table
	if !fb.Payload(&tbl) {
		return nil, nil
	}

	switch pt {
	case itrfb.CommandPayloadPeek:
		var p itrfb.Peek
		p.Init(tbl.Bytes, tbl.Pos)
		return Peek{Start: p.Start(), Length: p.Length()}, nil

	case itrfb.CommandPayloadGrep:
		var p itrfb.Grep
		p.Init(tbl.Bytes, tbl.Pos)
		return Grep{Pattern: string(p.Pattern()), MaxMatches: p.MaxMatches(), CaseInsensitive: p.CaseInsensitive()}, nil

	case itrfb.CommandPayloadPartition:
		var p itrfb.Partition
		p.Init(tbl.Bytes, tbl.Pos)
		return Partition{K: p.K(), Method: string(p.Method()), Overlap: p.Overlap(), Semantic: p.Semantic()}, nil

	case itrfb.CommandPayloadRecurse:
		var p itrfb.Recurse
		p.Init(tbl.Bytes, tbl.Pos)
		return Recurse{SubQuery: string(p.SubQuery()), ContextKey: string(p.ContextKey()), DepthHint: p.DepthHint()}, nil

	case itrfb.CommandPayloadToolExec:
		var p itrfb.ToolExec
		p.Init(tbl.Bytes, tbl.Pos)
		return ToolExec{ToolName: string(p.ToolName()), ArgsJSON: string(p.ArgsJson())}, nil

	case itrfb.CommandPayloadExecWasm:
		var p itrfb.ExecWasm
		p.Init(tbl.Bytes, tbl.Pos)
		return ExecWasm{ModuleKey: string(p.ModuleKey()), Entry: string(p.Entry()), InputJSON: string(p.InputJson())}, nil

	case itrfb.CommandPayloadFinal:
		var p itrfb.Final
		p.Init(tbl.Bytes, tbl.Pos)
		return Final{Answer: string(p.Answer()), VarName: string(p.VarName())}, nil

	case itrfb.CommandPayloadToolSearch:
		var p itrfb.ToolSearch
		p.Init(tbl.Bytes, tbl.Pos)
		return ToolSearch{Query: string(p.Query()), MaxResults: p.MaxResults()}, nil

	case itrfb.CommandPayloadCodeExec:
		var p itrfb.CodeExec
		p.Init(tbl.Bytes, tbl.Pos)
		return CodeExec{Language: string(p.Language()), Code: string(p.Code())}, nil

	case itrfb.CommandPayloadNONE:
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown FlatBuffers node payload type: %d", pt)
	}
}
