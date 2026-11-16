package flow

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The engine stays San-free so it can move to sdk-go; see the package doc.
func TestEngineImportsNothingFromSan(t *testing.T) {
	for _, file := range []string{"parse.go", "expand.go", "run.go", "view.go", "authoring.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), "github.com/genai-io/san/") {
				t.Errorf("%s imports %s", file, imp.Path.Value)
			}
		}
	}
}
