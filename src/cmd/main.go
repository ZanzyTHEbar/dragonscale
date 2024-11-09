package main

import (
	"fmt"

	"github.com/ZanzyTHEbar/decision-engine-go/llmpipeline"
)

func main() {
	pipeline := llmpipeline.NewLLMPipeline(nil, nil)
	query := "What is the temperature in San Francisco?"

	answer, err := pipeline.ProcessQuery(query)
	if err != nil {
		fmt.Printf("Error processing query: %v\n", err)
		return
	}

	fmt.Printf("Answer: %s\n", answer)
}
