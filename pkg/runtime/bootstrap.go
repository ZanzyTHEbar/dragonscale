package runtime

import (
	"context"
	"fmt"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/agent"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	dragonfantasy "github.com/ZanzyTHEbar/dragonscale/pkg/fantasy"
)

type OutboundMode string

const (
	OutboundModeNone     OutboundMode = "none"
	OutboundModeConsume  OutboundMode = "consume"
	OutboundModeDrop     OutboundMode = "drop"
	OutboundModeCallback OutboundMode = "callback"
)

type BootstrapOptions struct {
	Timeout          time.Duration
	OutboundMode     OutboundMode
	OutboundCallback func(bus.OutboundMessage)
	WrapModel        func(fantasy.LanguageModel) fantasy.LanguageModel
}

type RuntimeHandle struct {
	ctx       context.Context
	cancel    context.CancelFunc
	agentLoop *agent.AgentLoop
	msgBus    *bus.MessageBus
	outDone   chan struct{}
}

type kernelInvariantChecker interface {
	HasSecureBus() bool
	HasUnifiedRuntimeDeps() bool
}

func (h *RuntimeHandle) Context() context.Context {
	return h.ctx
}

func (h *RuntimeHandle) AgentLoop() *agent.AgentLoop {
	return h.agentLoop
}

func (h *RuntimeHandle) MessageBus() *bus.MessageBus {
	return h.msgBus
}

func (h *RuntimeHandle) Close() {
	if h.agentLoop != nil {
		h.agentLoop.Stop()
	}
	if h.cancel != nil {
		h.cancel()
	}
	if h.outDone != nil {
		<-h.outDone
	}
}

func Bootstrap(parent context.Context, cfg *config.Config, opts BootstrapOptions) (*RuntimeHandle, error) {
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := withExecutionContext(parent, opts.Timeout)

	provider, err := dragonfantasy.CreateProvider(cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("provider error: %w", err)
	}

	model, err := provider.LanguageModel(ctx, dragonfantasy.ModelID(cfg))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("model error: %w", err)
	}

	if opts.WrapModel != nil {
		model = opts.WrapModel(model)
	}

	msgBus := bus.NewMessageBus()
	outDone := startOutbound(msgBus, ctx, opts)

	agentLoop, err := agent.NewAgentLoop(ctx, cfg, msgBus, model)
	if err != nil {
		cancel()
		if outDone != nil {
			<-outDone
		}
		return nil, fmt.Errorf("agent loop init error: %w", err)
	}
	if err := validateKernelInvariants(agentLoop); err != nil {
		cancel()
		if outDone != nil {
			<-outDone
		}
		return nil, fmt.Errorf("agent loop init error: %w", err)
	}

	return &RuntimeHandle{
		ctx:       ctx,
		cancel:    cancel,
		agentLoop: agentLoop,
		msgBus:    msgBus,
		outDone:   outDone,
	}, nil
}

func validateKernelInvariants(loop kernelInvariantChecker) error {
	if loop == nil {
		return fmt.Errorf("agent loop is nil")
	}
	if !loop.HasSecureBus() {
		return fmt.Errorf("secure bus is not configured")
	}
	if !loop.HasUnifiedRuntimeDeps() {
		return fmt.Errorf("unified runtime dependencies are missing")
	}
	return nil
}

func withExecutionContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func startOutbound(msgBus *bus.MessageBus, ctx context.Context, opts BootstrapOptions) chan struct{} {
	mode := opts.OutboundMode
	if mode == "" || mode == OutboundModeNone {
		return nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, ok := msgBus.SubscribeOutbound(ctx)
			if !ok {
				return
			}

			switch mode {
			case OutboundModeConsume, OutboundModeDrop:
				// Intentionally no-op: consume and discard so producers never block.
			case OutboundModeCallback:
				if opts.OutboundCallback != nil {
					opts.OutboundCallback(msg)
				}
			default:
				// Unknown modes degrade safely to consume-and-discard.
			}
		}
	}()

	return done
}
