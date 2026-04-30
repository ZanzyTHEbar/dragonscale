package cortex

//go:generate go tool mockgen -source=tasks_rl.go -destination=mock_rl_store_test.go -package=cortex
//go:generate go tool mockgen -source=tasks_audit_analysis.go -destination=mock_audit_analysis_store_test.go -package=cortex
