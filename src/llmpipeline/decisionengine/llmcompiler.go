package decisionengine

type LLMCompiler struct{}

type Plan struct {
	Steps []string
}

type LLMCompilerProvider interface {
	GeneratePlan(agent Agent, query string) (Plan, error)
}

func NewLLMCompiler() *LLMCompiler {
	return &LLMCompiler{}
}

func (lc *LLMCompiler) GeneratePlan(agent Agent, query string) (Plan, error) {
	// Generate a plan
	return Plan{
		Steps: []string{"step1", "step2", "step3"},
	}, nil
}
