package queryprocessor

type DataBaseType string

type Router struct{}

type RouterProvider interface {
	Route(query string) (DataBaseType, error)
}

const (
	// DefaultDatabase is the default database to use when no other database is specified
	DEFAULT_DATABASE DataBaseType = "default"
	SQL_DATABASE     DataBaseType = "sql"
	VECTOR_DATABASE  DataBaseType = "vector"
	GRAPH_DATABASE   DataBaseType = "graph"
	HYBRID_DATABASE  DataBaseType = "hybrid"
	KV_DATABASE      DataBaseType = "kv"
)

func NewRouter() *Router {
	return &Router{}
}

// TODO: Implement the Route method
func (router *Router) Route(query string) (DataBaseType, error) {
	return DEFAULT_DATABASE, nil
}
