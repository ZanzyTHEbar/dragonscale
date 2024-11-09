package decisionengine

type TaskExecutor struct{}

type TaskExecutorProvider interface {
	ExecuteTasks(plan Plan) (string, error)
}

func NewTaskExecutor() *TaskExecutor {
	return &TaskExecutor{}

}

func (te *TaskExecutor) ExecuteTasks(plan Plan) (string, error) {
	// Execute the plan
	return "result", nil
}
