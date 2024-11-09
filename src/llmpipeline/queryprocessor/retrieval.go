package queryprocessor

type RetrievalPipeline struct{}

type RetrievalPipelineProvider interface {
	Retrieve(queries string, db DataBaseType) ([]string, error)
}

func NewRetrievalPipeline() *RetrievalPipeline {
	return &RetrievalPipeline{}
}

func (r *RetrievalPipeline) Retrieve(query string, db DataBaseType) ([]string, error) {
	// Retrieve the result
	return []string{"result1", "result2", "result3"}, nil
}
