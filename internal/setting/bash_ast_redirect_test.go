package setting

import (
	"slices"
	"testing"
)

func TestCompoundStatementRedirects(t *testing.T) {
	for _, command := range []string{
		"{ cat example.go; } > review-copy.go",
		"{ cat example.go; } >> review-copy.go",
		"(cat example.go) > review-copy.go",
		"(cat example.go) >> review-copy.go",
		"{ (cat example.go); } > review-copy.go",
		"({ cat example.go; } >> review-copy.go)",
		"{ { cat example.go; } > /dev/null; } > review-copy.go",
		"{ cat example.go; } &> review-copy.go",
		"(cat example.go) &>> review-copy.go",
		"{ cat example.go; } >| review-copy.go",
		"(cat example.go) <> review-copy.go",
		"{ cat example.go; } >& review-copy.go",
		"cat example.go >| review-copy.go",
		"cat example.go <> review-copy.go",
		"cat example.go >& review-copy.go",
		"cat example.go; > review-copy.go",
	} {
		t.Run(command, func(t *testing.T) {
			file := parseBashAST(command)
			if file == nil {
				t.Fatal("parse failed")
			}
			found := false
			for _, cmd := range extractCommandsAST(file) {
				found = found || slices.Contains(cmd.RedirPaths, "review-copy.go")
			}
			if !found {
				t.Fatal("output redirect lost during extraction")
			}
			if IsReadOnlyBashCommand(command) {
				t.Fatal("file output classified as read-only")
			}
		})
	}
}

func TestCompoundStatementReadOnlyRedirects(t *testing.T) {
	for _, command := range []string{
		"{ cat example.go; }",
		"(cat example.go)",
		"{ (cat example.go); } < example.go",
		"{ cat example.go; } > /dev/null",
		"(cat example.go) >> /dev/null",
		"{ (cat example.go); } 2> /dev/null",
		"{ { cat example.go; } >> /dev/null; } > /dev/null",
		"{ cat example.go; } >| /dev/null",
		"(cat example.go) <> /dev/null",
		"{ cat example.go; } >& /dev/null",
		"{ cat example.go; } 2>&1",
		"(cat example.go) 2>&1-",
		"cat example.go 2>&-",
	} {
		t.Run(command, func(t *testing.T) {
			if !IsReadOnlyBashCommand(command) {
				t.Fatal("read or safe /dev/null output rejected")
			}
		})
	}
}

func TestCompoundStatementSensitiveRedirect(t *testing.T) {
	file := parseBashAST("{ (cat example.go); } > /etc/passwd")
	if file == nil {
		t.Fatal("parse failed")
	}
	if checkASTSecurity(file) == "" {
		t.Fatal("compound redirect to sensitive path escaped security check")
	}
}
