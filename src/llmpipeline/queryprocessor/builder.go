package queryprocessor

type QueryBuilder struct{}

type QueryBuilderProvider interface {
	Build(originalQuery string, expandedQueries []string, rankedResults []RankedResult) (string, error)
}

func NewQueryBuilder() QueryBuilder {
	return QueryBuilder{}
}

func (qb QueryBuilder) Build(originalQuery string, expandedQueries []string, rankedResults []RankedResult) (string, error) {
	return originalQuery, nil
}
