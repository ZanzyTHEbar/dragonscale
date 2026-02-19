package securebus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/sipeed/picoclaw/pkg/security"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// ToolExecutor is the function signature the SecureBus calls to run a tool.
// In production this is tools.Registry.ExecuteWithContext. In tests it can
// be any function.
type ToolExecutor func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult

// CapabilitiesLookup returns the ToolCapabilities for a named tool.
// Wraps tools.Registry.Get + tools.ExtractCapabilities.
type CapabilitiesLookup func(toolName string) (tools.ToolCapabilities, bool)

// BusConfig configures the SecureBus.
type BusConfig struct {
	Policy PolicyConfig
	// Workers controls how many goroutines process requests concurrently.
	// 0 or 1 means single-goroutine (default, suitable for embedded).
	Workers int
}

// DefaultBusConfig returns a production-safe default configuration.
func DefaultBusConfig() BusConfig {
	return BusConfig{
		Policy:  DefaultPolicyConfig(),
		Workers: 1,
	}
}

// Bus is the SecureBus: the privilege boundary between the agent loop and
// tool execution. All tool calls flow through Bus.Execute.
//
// Pipeline for each call:
//  1. Decode request, extract tool capabilities
//  2. Policy validation (depth, recursion limits)
//  3. Secret injection into execution context
//  4. Execute tool via ToolExecutor
//  5. Scan output for leaks via Redactor
//  6. Write audit log entry
//  7. Return ToolResponse to caller
type Bus struct {
	cfg       BusConfig
	policy    *PolicyEngine
	secrets   *security.SecretStore // nil = no secret injection
	redactor  *security.Redactor
	audit     *AuditLog
	transport *ChannelTransport
	capLookup CapabilitiesLookup
	executor  ToolExecutor
	done      chan struct{}
}

// New creates a Bus and starts background worker goroutines.
// Call Close() to stop them.
func New(
	cfg BusConfig,
	secrets *security.SecretStore,
	capLookup CapabilitiesLookup,
	executor ToolExecutor,
	auditSinks ...AuditSink,
) *Bus {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	workers := cfg.Workers

	bus := &Bus{
		cfg:       cfg,
		policy:    NewPolicyEngine(cfg.Policy),
		secrets:   secrets,
		redactor:  security.NewRedactor(),
		audit:     NewAuditLog(auditSinks...),
		transport: NewChannelTransport(workers * 4),
		capLookup: capLookup,
		executor:  executor,
		done:      make(chan struct{}),
	}

	for i := 0; i < workers; i++ {
		go bus.runWorker()
	}
	return bus
}

// Transport returns the ChannelTransport so callers can send requests via Send.
func (b *Bus) Transport() Transport {
	return b.transport
}

// AuditLog returns the bus audit log for inspection.
func (b *Bus) AuditLog() *AuditLog {
	return b.audit
}

// Close shuts down the bus workers gracefully.
func (b *Bus) Close() {
	b.transport.Close()
	close(b.done)
}

// Execute is a convenience method for in-process callers that don't want to
// go through the Transport. It applies the full pipeline synchronously.
func (b *Bus) Execute(ctx context.Context, req itr.ToolRequest) itr.ToolResponse {
	return b.dispatch(ctx, req)
}

// runWorker reads from the transport channel and dispatches requests.
func (b *Bus) runWorker() {
	for {
		select {
		case env, ok := <-b.transport.Requests():
			if !ok {
				return
			}
			resp := b.dispatch(context.Background(), env.req)
			env.reply(resp, nil)
		case <-b.done:
			return
		}
	}
}

// dispatch runs the full pipeline for a single request.
func (b *Bus) dispatch(ctx context.Context, req itr.ToolRequest) itr.ToolResponse {
	start := time.Now()

	event := AuditEvent{
		RequestID:   req.ID,
		SessionKey:  req.SessionKey,
		ToolCallID:  req.ToolCallID,
		CommandType: string(req.Type),
		Depth:       req.Depth,
		At:          start,
	}

	// Only ToolExec requests require capability/secret/leak checks.
	// RLM operations (Peek, Grep, etc.) are structural and access no tools.
	if req.Type != itr.CmdToolExec {
		resp := b.handleRLMCommand(ctx, req)
		event.DurationMS = time.Since(start).Milliseconds()
		_ = b.audit.Append(event)
		return resp
	}

	te, ok := req.Payload.(itr.ToolExec)
	if !ok {
		event.IsError = true
		event.DurationMS = time.Since(start).Milliseconds()
		_ = b.audit.Append(event)
		return itr.NewErrorResponse(req.ID, "internal: payload is not ToolExec")
	}
	event.ToolName = te.ToolName

	// 1. Capability lookup
	caps, found := b.capLookup(te.ToolName)
	if !found {
		caps = tools.ZeroCapabilities()
	}

	// 2. Policy validation
	if err := b.policy.Validate(req, caps); err != nil {
		event.IsError = true
		event.PolicyViolation = err.Error()
		event.DurationMS = time.Since(start).Milliseconds()
		_ = b.audit.Append(event)
		return itr.NewErrorResponse(req.ID, "policy violation: "+err.Error())
	}

	// 3. Deserialise args — always produce a non-nil map for safe injection.
	args := make(map[string]interface{})
	if te.ArgsJSON != "" && te.ArgsJSON != "null" {
		if err := json.Unmarshal([]byte(te.ArgsJSON), &args); err != nil {
			event.IsError = true
			event.DurationMS = time.Since(start).Milliseconds()
			_ = b.audit.Append(event)
			return itr.NewErrorResponse(req.ID, fmt.Sprintf("invalid args JSON: %v", err))
		}
	}

	// 4. Secret injection
	injectedSecrets, err := b.injectSecrets(ctx, caps.Secrets, args)
	if err != nil {
		event.IsError = true
		event.DurationMS = time.Since(start).Milliseconds()
		_ = b.audit.Append(event)
		return itr.NewErrorResponse(req.ID, "secret injection failed: "+err.Error())
	}
	event.SecretsAccessed = injectedSecrets

	// 5. Execute
	result := b.executor(ctx, te.ToolName, args)

	// 6. Leak scan
	var resp itr.ToolResponse
	if result == nil {
		resp = itr.NewSuccessResponse(req.ID, "", 0)
	} else {
		resultText := result.ForLLM
		if b.redactor.ContainsSensitive(resultText) {
			redacted := b.redactor.Redact(resultText)
			resp = itr.NewLeakResponse(req.ID, redacted, nil)
			event.LeakDetected = true
		} else {
			resp = itr.NewSuccessResponse(req.ID, resultText, 0)
		}
		if result.IsError {
			resp.IsError = true
		}
	}

	event.IsError = resp.IsError
	event.DurationMS = time.Since(start).Milliseconds()
	_ = b.audit.Append(event)
	return resp
}

// injectSecrets resolves required secrets and injects them into args.
// Returns the names of secrets accessed. Skips missing optional secrets.
func (b *Bus) injectSecrets(_ context.Context, refs []tools.SecretRef, args map[string]interface{}) ([]string, error) {
	if b.secrets == nil || len(refs) == 0 {
		return nil, nil
	}

	var accessed []string
	for _, ref := range refs {
		val, err := b.secrets.Get(ref.Name)
		if err != nil {
			if err == security.ErrSecretNotFound && !ref.Required {
				continue
			}
			return accessed, fmt.Errorf("secret %q: %w", ref.Name, err)
		}
		accessed = append(accessed, ref.Name)
		injectArg(args, ref.InjectAs, string(val))
	}
	return accessed, nil
}

// injectArg places the secret value into the args map according to the InjectAs spec.
// Currently only "arg:<key>" injection is applied at the args level.
// "env:*" and "header:*" injections are handled by the tool itself or HTTP client.
func injectArg(args map[string]interface{}, injectAs, value string) {
	const argPrefix = "arg:"
	if args == nil {
		return
	}
	if len(injectAs) > len(argPrefix) && injectAs[:len(argPrefix)] == argPrefix {
		key := injectAs[len(argPrefix):]
		args[key] = value
	}
	// "env:" and "header:" variants require tool cooperation; they are
	// recorded in the capability manifest so auditing can trace what was accessed.
}

// handleRLMCommand processes structural RLM decomposition commands.
// These commands don't invoke tool code — they operate on the context rope
// managed by the RLMEngine (which calls the SecureBus, not the other way around).
func (b *Bus) handleRLMCommand(_ context.Context, req itr.ToolRequest) itr.ToolResponse {
	// RLM commands are executed by the RLMEngine; if they reach the SecureBus
	// directly it means the engine called Bus.Execute with an RLM payload.
	// Return a stub response — the RLMEngine interprets this.
	switch req.Type {
	case itr.CmdFinal:
		if f, ok := req.Payload.(itr.Final); ok {
			return itr.NewSuccessResponse(req.ID, f.Answer, 0)
		}
	}
	return itr.NewErrorResponse(req.ID, fmt.Sprintf("RLM command %q not handled at bus level", req.Type))
}
