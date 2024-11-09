package queryprocessor

type Expander struct{}

type ExpanderProvider interface {
	Expand(query string) ([]string, error)
}

func NewExpander() *Expander {
	return &Expander{}
}

// TODO: Implement the Expand method
func (e *Expander) Expand(query string) ([]string, error) {
	return []string{query}, nil
}
