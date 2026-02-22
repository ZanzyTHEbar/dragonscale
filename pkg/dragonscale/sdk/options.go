package sdk

type AgentOptions struct {
	Message    string
	SessionKey string
	Debug      bool
}

type GatewayOptions struct {
	Debug bool
}

type MigrateOptions struct {
	DryRun          bool
	ConfigOnly      bool
	WorkspaceOnly   bool
	Force           bool
	Refresh         bool
	OpenClawHome    string
	DragonscaleHome string
}

type CronAddOptions struct {
	Name    string
	Message string
	EveryMS *int64
	Cron    string
	Deliver bool
	To      string
	Channel string
}
