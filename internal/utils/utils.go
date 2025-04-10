package utils

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// FormatDuration formats a duration in a human-readable way
func FormatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%d µs", d.Microseconds())
	} else if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	} else if d < time.Minute {
		return fmt.Sprintf("%.2f s", d.Seconds())
	} else if d < time.Hour {
		return fmt.Sprintf("%.2f min", d.Minutes())
	}
	return fmt.Sprintf("%.2f h", d.Hours())
}

// CreateDirIfNotExists creates a directory if it doesn't exist
func CreateDirIfNotExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	return nil
}

// generateEventID creates a unique identifier for an event.
func GenerateEventID() string {
	return uuid.NewString() + "-" + time.Now().Format("20060102150405")
}