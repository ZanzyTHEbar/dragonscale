package agent

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
)

// ContentType represents the detected type of content for summarization.
type ContentType string

const (
	ContentTypeJSON  ContentType = "json"
	ContentTypeCode  ContentType = "code"
	ContentTypeCSV   ContentType = "csv"
	ContentTypeText  ContentType = "text"
	ContentTypeError ContentType = "error"
)

// TypeSummary contains a type-aware summary of content.
type TypeSummary struct {
	ContentType ContentType `json:"content_type"`
	Summary     string      `json:"summary"`
	Details     any         `json:"details,omitempty"`
}

// SummarizeContent detects content type and generates an appropriate summary.
// This replaces blind truncation with meaningful previews based on content structure.
func SummarizeContent(content string, maxLen int) TypeSummary {
	content = strings.TrimSpace(content)
	if content == "" {
		return TypeSummary{ContentType: ContentTypeText, Summary: "(empty)"}
	}

	// Check for error patterns first (they can appear in any content type)
	if isError(content) {
		return summarizeError(content, maxLen)
	}

	// Try JSON first (most specific)
	if isJSON(content) {
		return summarizeJSON(content, maxLen)
	}

	// Try CSV
	if isCSV(content) {
		return summarizeCSV(content, maxLen)
	}

	// Try code (heuristic-based)
	if isCode(content) {
		return summarizeCode(content, maxLen)
	}

	// Default to text summarization
	return summarizeText(content, maxLen)
}

// isJSON checks if content appears to be valid JSON.
func isJSON(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false
	}
	var js any
	if err := json.Unmarshal([]byte(trimmed), &js); err != nil {
		return false
	}
	return true
}

// summarizeJSON creates a JSON schema summary (keys for objects, length for arrays).
func summarizeJSON(content string, maxLen int) TypeSummary {
	trimmed := strings.TrimSpace(content)

	var data any
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return TypeSummary{ContentType: ContentTypeJSON, Summary: truncateRunes(content, maxLen)}
	}

	switch v := data.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		schema := fmt.Sprintf("JSON object with %d fields: %s", len(keys), strings.Join(keys, ", "))
		if len(schema) > maxLen {
			schema = schema[:maxLen-3] + "..."
		}
		return TypeSummary{
			ContentType: ContentTypeJSON,
			Summary:     schema,
			Details:     map[string]any{"keys": keys, "type": "object", "field_count": len(v)},
		}

	case []any:
		// Show first few items as sample
		sampleCount := min(len(v), 3)
		var sampleItems []string
		for i := 0; i < sampleCount; i++ {
			switch item := v[i].(type) {
			case map[string]any:
				itemKeys := make([]string, 0, len(item))
				for k := range item {
					itemKeys = append(itemKeys, k)
				}
				sampleItems = append(sampleItems, fmt.Sprintf("{%d fields}", len(itemKeys)))
			case string:
				sampleItems = append(sampleItems, fmt.Sprintf("%q", truncateRunes(item, 30)))
			default:
				sampleItems = append(sampleItems, fmt.Sprintf("%v", item))
			}
		}

		summary := fmt.Sprintf("JSON array with %d items", len(v))
		if len(sampleItems) > 0 {
			summary += fmt.Sprintf(" [sample: %s]", strings.Join(sampleItems, ", "))
		}
		summary = truncateSummary(summary, maxLen)
		return TypeSummary{
			ContentType: ContentTypeJSON,
			Summary:     summary,
			Details:     map[string]any{"length": len(v), "type": "array", "sample_count": sampleCount},
		}

	default:
		return TypeSummary{ContentType: ContentTypeJSON, Summary: fmt.Sprintf("JSON scalar: %v", v)}
	}
}

// isError checks if content contains error patterns like stack traces,
// exception messages, or failure indicators at the start of lines.
func isError(content string) bool {
	errorPatterns := []string{
		"error:", "exception:", "stack trace", "panic:", "fail:",
		"runtime error", "fatal:", "warning:", "syntax error",
		"compilation error", "build failed", "test failed",
	}

	lines := strings.Split(content, "\n")
	errorCount := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// Check for error patterns at the start of lines
		for _, pattern := range errorPatterns {
			if strings.HasPrefix(lower, pattern) {
				errorCount++
				break
			}
		}

		// Additional check for common error indicators anywhere in line
		if strings.Contains(lower, "--- stack trace ---") ||
			strings.Contains(lower, "caused by:") ||
			strings.Contains(lower, "at line") ||
			strings.Contains(lower, "error code:") {
			errorCount++
		}
	}

	// Require at least 2 error indicators or 1 strong indicator
	return errorCount >= 2 ||
		strings.Contains(strings.ToLower(content), "panic:") ||
		strings.Contains(strings.ToLower(content), "stack trace")
}

// summarizeError creates a summary of error content, extracting key error messages.
func summarizeError(content string, maxLen int) TypeSummary {
	lines := strings.Split(content, "\n")

	var errorMessages []string
	var contextLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)

		// Capture error message lines
		if strings.HasPrefix(lower, "error:") ||
			strings.HasPrefix(lower, "exception:") ||
			strings.HasPrefix(lower, "panic:") ||
			strings.HasPrefix(lower, "fail:") ||
			strings.HasPrefix(lower, "fatal:") ||
			strings.HasPrefix(lower, "warning:") {
			errorMessages = append(errorMessages, trimmed)
		} else if len(contextLines) < 3 && !strings.Contains(lower, "at ") {
			// Capture first few non-stack-trace lines as context
			contextLines = append(contextLines, trimmed)
		}
	}

	var summary string
	if len(errorMessages) > 0 {
		summary = "Error: " + errorMessages[0]
		if len(errorMessages) > 1 {
			summary += fmt.Sprintf(" (%d errors)", len(errorMessages))
		}
	} else if len(contextLines) > 0 {
		summary = "Error context: " + contextLines[0]
	} else {
		summary = "Error detected in content"
	}

	// Include line count info
	lineCount := len(lines)
	summary = fmt.Sprintf("%s [%d lines]", summary, lineCount)

	summary = truncateSummary(summary, maxLen)

	return TypeSummary{
		ContentType: ContentTypeError,
		Summary:     summary,
		Details: map[string]any{
			"error_count": len(errorMessages),
			"line_count":  lineCount,
			"errors":      errorMessages[:min(len(errorMessages), 5)],
			"has_panic":   strings.Contains(strings.ToLower(content), "panic:"),
			"has_trace":   strings.Contains(strings.ToLower(content), "stack trace"),
		},
	}
}

// isCSV checks if content appears to be CSV format.
// Uses multiple heuristics to reduce false positives on code with commas:
// - Header detection: first line should look like headers, not code
// - Numeric data check: subsequent lines should contain numeric values
// - Minimum 2 columns consistently across lines
func isCSV(content string) bool {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) < 2 {
		return false
	}

	// Filter out empty lines for processing
	var nonEmptyLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}
	if len(nonEmptyLines) < 2 {
		return false
	}

	// Use at most 5 non-empty lines for analysis
	checkLines := nonEmptyLines
	if len(checkLines) > 5 {
		checkLines = checkLines[:5]
	}

	// Check for consistent comma count (minimum 2 columns = at least 1 comma)
	commaCounts := make([]int, len(checkLines))
	for i, line := range checkLines {
		commaCounts[i] = strings.Count(line, ",")
	}

	// Require at least 2 columns (1 comma) in all lines
	for _, count := range commaCounts {
		if count < 1 {
			return false
		}
	}

	// Check for consistent column count (allowing for trailing commas)
	firstCount := commaCounts[0]
	consistent := 0
	for _, count := range commaCounts[1:] {
		if count == firstCount || count == firstCount-1 {
			consistent++
		}
	}
	if consistent < len(commaCounts)-1 {
		return false
	}

	// Header detection: first line should look like headers
	// Headers typically don't contain common code patterns
	header := checkLines[0]
	headerLower := strings.ToLower(header)

	// Reject headers that look like code statements
	codePatterns := []string{
		"func ", "def ", "class ", "if ", "for ", "while ", "return",
		"import ", "package ", "#include", "const ", "var ", "let ",
		"public ", "private ", "protected ", "static ", "void ",
	}
	for _, pattern := range codePatterns {
		if strings.HasPrefix(headerLower, pattern) {
			return false
		}
	}

	// Headers typically have balanced alphanumeric content
	// Count alphanumeric vs special chars in header fields
	fields := strings.Split(header, ",")
	if len(fields) < 2 {
		return false
	}

	headerLooksValid := true
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed == "" {
			continue
		}
		// Headers should contain mostly letters, not symbols
		letterCount := 0
		symbolCount := 0
		for _, r := range trimmed {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				letterCount++
			} else if r == '(' || r == ')' || r == '{' || r == '}' || r == ';' || r == '=' {
				symbolCount++
			}
		}
		// If field has more symbols than letters, likely code
		if symbolCount > letterCount/2 {
			headerLooksValid = false
			break
		}
	}
	if !headerLooksValid {
		return false
	}

	// Check data lines for CSV-like content (numeric values, quoted strings)
	if len(checkLines) >= 2 {
		dataLines := checkLines[1:]
		numericLineCount := 0

		for _, line := range dataLines {
			dataFields := strings.Split(line, ",")
			hasNumericField := false
			for _, field := range dataFields {
				trimmed := strings.TrimSpace(field)
				if trimmed == "" {
					continue
				}
				// Check if field looks like a number (integer, float, or quoted)
				if (trimmed[0] >= '0' && trimmed[0] <= '9') ||
					strings.HasPrefix(trimmed, "\"") ||
					strings.HasPrefix(trimmed, "'") ||
					trimmed == "true" || trimmed == "false" ||
					trimmed == "null" || trimmed == "NA" {
					hasNumericField = true
					break
				}
			}
			if hasNumericField {
				numericLineCount++
			}
		}

		// At least half of data lines should have CSV-like values
		if numericLineCount < len(dataLines)/2 {
			return false
		}
	}

	return true
}

// summarizeCSV creates a CSV header and stats summary.
func summarizeCSV(content string, maxLen int) TypeSummary {
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1 // Allow variable

	// Read header
	header, err := reader.Read()
	if err != nil {
		return TypeSummary{ContentType: ContentTypeCSV, Summary: truncateRunes(content, maxLen)}
	}

	// Count rows
	rowCount := 0
	for {
		_, err := reader.Read()
		if err != nil {
			break
		}
		rowCount++
	}

	summary := fmt.Sprintf("CSV with %d columns: %s (%d data rows)",
		len(header), strings.Join(header, ", "), rowCount)

	summary = truncateSummary(summary, maxLen)

	return TypeSummary{
		ContentType: ContentTypeCSV,
		Summary:     summary,
		Details:     map[string]any{"columns": header, "column_count": len(header), "row_count": rowCount},
	}
}

// isCode uses heuristics to detect if content is source code.
func isCode(content string) bool {
	codeIndicators := []string{
		"func ", "def ", "class ", "import ", "package ",
		"#include", "public class", "private ", "function ",
		"const ", "let ", "var ", "=> ", ":= ",
	}

	lines := strings.Split(content, "\n")
	indicatorCount := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, indicator := range codeIndicators {
			if strings.HasPrefix(trimmed, indicator) {
				indicatorCount++
				break
			}
		}
	}

	// If more than 3 code-like lines, consider it code
	return indicatorCount > 3
}

// summarizeCode creates a code structure summary (functions, classes, imports).
func summarizeCode(content string, maxLen int) TypeSummary {
	lines := strings.Split(content, "\n")

	var functions, classes, imports []string
	braceDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track brace depth for scope
		for _, r := range trimmed {
			switch r {
			case '{', '(', '[':
				braceDepth++
			case '}', ')', ']':
				braceDepth--
			}
		}

		// Top-level declarations
		if braceDepth == 0 || braceDepth == 1 {
			if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
				strings.HasPrefix(trimmed, "function ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					name := parts[1]
					if idx := strings.Index(name, "("); idx > 0 {
						name = name[:idx]
					}
					functions = append(functions, name)
				}
			}
			if strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "public class ") ||
				strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, "struct") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					name := parts[1]
					if idx := strings.Index(name, "{"); idx > 0 {
						name = name[:idx]
					}
					classes = append(classes, name)
				}
			}
			if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "#include") ||
				strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "using ") {
				imports = append(imports, trimmed)
			}
		}
	}

	summaryParts := []string{}
	if len(functions) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d functions", len(functions)))
	}
	if len(classes) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d types/classes", len(classes)))
	}
	if len(imports) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d imports", len(imports)))
	}

	summary := "Code: " + strings.Join(summaryParts, ", ")
	if len(functions) > 0 {
		funcList := strings.Join(functions[:min(len(functions), 5)], ", ")
		if len(functions) > 5 {
			funcList += "..."
		}
		summary += fmt.Sprintf(" [funcs: %s]", funcList)
	}

	summary = truncateSummary(summary, maxLen)

	return TypeSummary{
		ContentType: ContentTypeCode,
		Summary:     summary,
		Details: map[string]any{
			"functions":      functions,
			"classes":        classes,
			"imports":        len(imports),
			"function_count": len(functions),
		},
	}
}

// summarizeText creates a meaningful text excerpt (first + last parts).
func summarizeText(content string, maxLen int) TypeSummary {
	lines := strings.Split(content, "\n")

	// Get first few non-empty lines
	var firstLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			firstLines = append(firstLines, line)
			if len(firstLines) >= 3 {
				break
			}
		}
	}

	// Get last few non-empty lines
	var lastLines []string
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastLines = append([]string{lines[i]}, lastLines...)
			if len(lastLines) >= 2 {
				break
			}
		}
	}

	var summary string
	if len(firstLines) > 0 {
		summary = strings.Join(firstLines, "\n")
		if len(lastLines) > 0 && len(lines) > len(firstLines)+len(lastLines) {
			summary += "\n...\n" + strings.Join(lastLines, "\n")
		}
	}

	// Word count
	wordCount := len(strings.Fields(content))
	summary = fmt.Sprintf("Text (%d words): %s", wordCount, summary)

	summary = truncateSummary(summary, maxLen)

	return TypeSummary{
		ContentType: ContentTypeText,
		Summary:     summary,
		Details:     map[string]any{"word_count": wordCount, "line_count": len(lines)},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// truncateSummary safely truncates a string to maxLen, adding "..." when there's room.
func truncateSummary(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > 3 {
		return s[:maxLen-3] + "..."
	}
	return s[:maxLen]
}
