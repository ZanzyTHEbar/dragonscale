package decisionengine

import "fmt"

// DecisionEngine - Interface for the Decision Engine
type DecisionEngine interface {
	Execute(query string) (string, error)
}

type engine struct {
	agentManager AgentManagerProvider
	compiler     LLMCompilerProvider
	executor     TaskExecutorProvider
}

func NewDecisionEngine(decisionEngine DecisionEngine) DecisionEngine {
	if decisionEngine != nil {
		return decisionEngine
	}

	return &engine{
		agentManager: NewAgentManager(),
		compiler:     NewLLMCompiler(),
		executor:     NewTaskExecutor(),
	}
}

func (de *engine) Execute(query string) (string, error) {
	// Step 1: Select the appropriate agent based on the complexity of the query
	agent := de.agentManager.GetAgent(len(query))

	// Step 2: Generate a plan using the selected agent - the plan is then turned into a Directed Acyclic Graph (DAG) for the TaskExecutor to read.
	plan, err := de.compiler.GeneratePlan(agent, query)
	if err != nil {
		return "", fmt.Errorf("error generating plan: %v", err)
	}

	// Step 3: Execute the plan using the TaskExecutor
	result, err := de.executor.ExecuteTasks(plan)
	if err != nil {
		return "", fmt.Errorf("error executing plan: %v", err)
	}

	return result, nil
}
