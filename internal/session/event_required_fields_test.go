package session

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

type requiredFieldFixture struct {
	Required string            `json:"required"`
	Omitted  bool              `json:"omitted,omitempty"`
	Zero     bool              `json:"zero,omitzero"`
	Mixed    string            `json:"mixed,string,omitempty"`
	Values   []string          `json:"values"`
	Extra    map[string]string `json:"extra"`
	Ignored  string            `json:"-"`
}

func TestRequiredEventFieldCachePreservesValidation(t *testing.T) {
	for _, test := range []struct {
		name, input, failure string
	}{
		{"optional", `{"required":"ok","values":[],"extra":{}}`, ""},
		{"nullable-collections", `{"required":"ok","values":null,"extra":null}`, ""},
		{"missing-scalar", `{"values":[],"extra":{}}`, `missing required field "required"`},
		{"null-scalar", `{"required":null,"values":[],"extra":{}}`, `null required field "required"`},
		{"missing-slice", `{"required":"ok","extra":{}}`, `missing required field "values"`},
		{"missing-map", `{"required":"ok","values":[]}`, `missing required field "extra"`},
		{"malformed", `{`, "unexpected end"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for range 2 {
				err := requiredEventFields([]byte(test.input), reflect.TypeFor[requiredFieldFixture]())
				if test.failure == "" && err != nil || test.failure != "" && (err == nil || !strings.Contains(err.Error(), test.failure)) {
					t.Fatalf("validation changed: got %v, want %q", err, test.failure)
				}
			}
		})
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if err := requiredEventFields([]byte(`{"required":"ok","values":null,"extra":null}`), reflect.TypeFor[*requiredFieldFixture]()); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestOptionalJSONTagMatchesWholeOptions(t *testing.T) {
	for _, options := range []string{"omitempty", "omitzero", "string,omitempty", "omitzero,string"} {
		if !optionalJSONTag(options) {
			t.Fatalf("optional tag rejected: %q", options)
		}
	}
	for _, options := range []string{"", "string", "omitemptyish", "string,notomitzero", ",,"} {
		if optionalJSONTag(options) {
			t.Fatalf("required tag made optional: %q", options)
		}
	}
}
