package queryprocessor

import "sync"

type QueryProcessor interface {
	Process(query string) (string, error)
}

type processor struct {
	analyzer     AnalyzerProvider
	expander     ExpanderProvider
	router       RouterProvider
	retrieval    RetrievalPipelineProvider
	crossEncoder CrossEncoderProvider
	queryBuilder QueryBuilderProvider
}

func NewQueryProcessor(queryProcessor QueryProcessor) QueryProcessor {
	if queryProcessor != nil {
		return queryProcessor
	}

	return &processor{
		analyzer:     NewAnalyzer(),
		expander:     NewExpander(),
		router:       NewRouter(),
		retrieval:    NewRetrievalPipeline(),
		crossEncoder: NewCrossEncoder(),
		queryBuilder: NewQueryBuilder(),
	}
}

// Process is the main method of the QueryProcessor. It takes a query as input and returns a refined query.
func (p *processor) Process(query string) (string, error) {
	// Step 1: Analyze the query
	analyzedQuery, err := p.analyzer.Analyze(query)
	if err != nil {
		return "", err
	}

	// Step 2: Expand the query in parallel
	expandedQueries, err := p.expander.Expand(analyzedQuery)
	if err != nil {
		return "", err
	}

	// Step 3: Retrieve and rank the results concurrently for each expanded query
	var wg sync.WaitGroup
	resultsChan := make(chan []RankedResult, len(expandedQueries))

	for _, expandedQuery := range expandedQueries {
		wg.Add(1)
		go func(expandedQuery string) {
			defer wg.Done()
			db, err := p.router.Route(expandedQuery)
			if err != nil {
				return
			}

			results, err := p.retrieval.Retrieve(expandedQuery, db)
			ranked := p.crossEncoder.RankResults(results)
			resultsChan <- ranked
		}(expandedQuery)
	}

	// Step 4: Wait for all goroutines to finish and combine the results
	go func() {
		wg.Wait()
		close(resultsChan) // TODO: FIX THIS with sync.Once
	}()

	var allResults []RankedResult
	for results := range resultsChan {
		allResults = append(allResults, results...)
	}

	// Step 5: Build the refined final query
	refinedQuery, err := p.queryBuilder.Build(query, expandedQueries, allResults)
	if err != nil {
		return "", err
	}

	return refinedQuery, nil
}
