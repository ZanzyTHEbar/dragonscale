package tools

import (
	"fmt"
	"time"
)

const (
	DAGRecoveryKVPrefix   = "dag_recovery:"
	DAGRecoveryNodePrefix = "recovery-"
)

type DAGRecoveryRecord struct {
	NodeID        string    `json:"node_id"`
	SessionKey    string    `json:"session_key"`
	OriginalIndex int       `json:"original_index"`
	Role          string    `json:"role"`
	Content       string    `json:"content"`
	TokenEstimate int       `json:"token_estimate"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
}

func DAGRecoveryKVKey(sessionKey, nodeID string) string {
	return fmt.Sprintf("%s%s:%s", DAGRecoveryKVPrefix, sessionKey, nodeID)
}
