package sdk

type AgentOptions struct {
	Message    string
	SessionKey string
	Debug      bool
}

type GatewayOptions struct {
	Debug bool
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
