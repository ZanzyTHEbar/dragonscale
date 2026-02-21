package store

import (
	"fmt"
	"strings"
)

func contentAtLeastTokens(target int) string {
	if target <= 0 {
		return ""
	}

	var b strings.Builder
	for i := 0; estimateTokens(b.String()) < target; i++ {
		b.WriteString(fmt.Sprintf("token-%d ", i))
	}
	return b.String()
}
