package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up017AgentAuditOutcomes, down017AgentAuditOutcomes)
}

func up017AgentAuditOutcomes(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`ALTER TABLE agent_audit_log ADD COLUMN success BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE agent_audit_log ADD COLUMN error_msg TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_audit_log ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT ''`,
		`UPDATE agent_audit_log
		 SET success = CASE
			WHEN lower(action) = 'tool_error'
			  OR instr(lower(action), 'error') > 0
			  OR instr(lower(action), 'fail') > 0
			THEN FALSE
			ELSE TRUE
		 END`,
		`UPDATE agent_audit_log
		 SET error_msg = COALESCE(output, '')
		 WHERE success = FALSE
		   AND error_msg = ''`,
		`CREATE INDEX IF NOT EXISTS idx_audit_tool_call_id ON agent_audit_log(tool_call_id)`,
	}

	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("017_agent_audit_outcomes up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down017AgentAuditOutcomes(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_audit_tool_call_id`); err != nil {
		return fmt.Errorf("017_agent_audit_outcomes down: %w", err)
	}
	return nil
}
