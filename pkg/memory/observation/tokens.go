package observation

import (
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

var (
	encoderOnce sync.Once
	encoder     *tiktoken.Tiktoken
)

func getEncoder() *tiktoken.Tiktoken {
	encoderOnce.Do(func() {
		enc, err := tiktoken.EncodingForModel("gpt-4")
		if err != nil {
			enc, _ = tiktoken.GetEncoding("cl100k_base")
		}
		encoder = enc
	})
	return encoder
}

// EstimateTokens returns a token count estimate for the given text.
// Falls back to len(text)/4 if tiktoken is unavailable.
func EstimateTokens(text string) int {
	enc := getEncoder()
	if enc == nil {
		return len(text) / 4
	}
	return len(enc.Encode(text, nil, nil))
}

// EstimateMessagesTokens estimates the total token count for a slice of
// role+content message pairs. Adds ~4 tokens overhead per message for
// role markers and delimiters.
func EstimateMessagesTokens(messages []MessagePair) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.Content) + 4
	}
	return total
}

// MessagePair is a minimal role+content pair for token estimation.
type MessagePair struct {
	Role    string
	Content string
}
