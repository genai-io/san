package command

import (
	"fmt"
	"strings"
	"unicode"
)

// Argument retains the original byte range so completion can replace a field
// without changing quoted values or whitespace in earlier arguments.
type Argument struct {
	Value      string
	Start, End int
}

// ScanArguments splits on whitespace outside single or double quotes. Quotes
// are removed from Value; adjacent quoted and unquoted parts form one field.
// An unfinished quote returns the partial field as well as an error, allowing
// completion to match a name while it is still being typed.
func ScanArguments(args string) ([]Argument, error) {
	var fields []Argument
	var cur strings.Builder
	var quote rune
	start := -1
	for at, r := range args {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case unicode.IsSpace(r):
			if start >= 0 {
				fields = append(fields, Argument{Value: cur.String(), Start: start, End: at})
				cur.Reset()
				start = -1
			}
		default:
			if start < 0 {
				start = at
			}
			if r == '"' || r == '\'' {
				quote = r
			} else {
				cur.WriteRune(r)
			}
		}
	}
	if start >= 0 {
		fields = append(fields, Argument{Value: cur.String(), Start: start, End: len(args)})
	}
	if quote != 0 {
		return fields, fmt.Errorf("unclosed %c quote", quote)
	}
	return fields, nil
}

// SplitArguments is the strict execution counterpart to ScanArguments.
func SplitArguments(args string) ([]string, error) {
	fields, err := ScanArguments(args)
	if err != nil {
		return nil, err
	}
	var values []string
	for _, field := range fields {
		values = append(values, field.Value)
	}
	return values, nil
}

// QuoteArgument encodes a value as one argument for SplitArguments. Literal
// double quotes use a single-quoted segment, preserving backslashes unchanged.
func QuoteArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\''
	}) < 0 {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `"'"'"`) + `"`
}
