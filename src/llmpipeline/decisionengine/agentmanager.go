package decisionengine

type AgentManager struct{}

type Agent struct{}

type AgentManagerProvider interface {
	GetAgent(queryLength int) Agent
}

func NewAgentManager() *AgentManager {
	return &AgentManager{}
}

func (am *AgentManager) GetAgent(queryLength int) Agent {
	// Select the appropriate agent based on the complexity of the query
	return Agent{}
}
