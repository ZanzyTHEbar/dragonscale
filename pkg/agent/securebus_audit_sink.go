package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
)

type secureBusAuditSink struct {
	enqueue func(*memory.AuditEntry) bool
}

func newSecureBusAuditSink(enqueue func(*memory.AuditEntry) bool) securebus.AuditSink {
	if enqueue == nil {
		return nil
	}
	return &secureBusAuditSink{enqueue: enqueue}
}

func (s *secureBusAuditSink) Write(event securebus.AuditEvent) error {
	if s == nil || s.enqueue == nil {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	output := string(payload)
	action := secureBusAuditAction(event)
	target := strings.TrimSpace(event.ToolName)
	if target == "" {
		target = strings.TrimSpace(event.CommandType)
	}
	entry := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    pkg.NAME,
		SessionKey: strings.TrimSpace(event.SessionKey),
		Action:     action,
		Target:     target,
		ToolCallID: strings.TrimSpace(event.ToolCallID),
		Input:      strings.TrimSpace(event.ToolInput),
		Output:     output,
		Success:    !event.IsError && strings.TrimSpace(event.PolicyViolation) == "",
		ErrorMsg:   secureBusAuditError(event),
		DurationMS: int(event.DurationMS),
		CreatedAt:  event.At,
	}
	if !s.enqueue(entry) {
		return fmt.Errorf("securebus audit enqueue failed")
	}
	return nil
}

func secureBusAuditAction(event securebus.AuditEvent) string {
	commandType := strings.TrimSpace(event.CommandType)
	base := "securebus_event"
	if commandType != "" {
		base = "securebus_" + commandType
	}
	if strings.TrimSpace(event.PolicyViolation) != "" {
		return base + "_policy_violation"
	}
	if event.LeakDetected {
		return base + "_leak"
	}
	if event.IsError {
		return base + "_error"
	}
	return base
}

func secureBusAuditError(event securebus.AuditEvent) string {
	if msg := strings.TrimSpace(event.PolicyViolation); msg != "" {
		return msg
	}
	return strings.TrimSpace(event.ExecutionError)
}
