package command

import (
	"slices"
	"testing"
)

func TestSplitArguments(t *testing.T) {
	for _, tc := range []struct {
		args string
		want []string
	}{
		{"", nil},
		{`review base="main branch" note='say "hi"' x=`, []string{"review", "base=main branch", `note=say "hi"`, "x="}},
		{`"" '' key=""`, []string{"", "", "key="}},
		{"\t\"发布 审查\"\u2003base=main\t", []string{"发布 审查", "base=main"}},
		{`topic="two "words`, []string{"topic=two words"}},
		{`path="C:\work tree"`, []string{`path=C:\work tree`}},
	} {
		got, err := SplitArguments(tc.args)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("SplitArguments(%q) = %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}
	if _, err := SplitArguments(`topic="two `); err == nil {
		t.Fatal("execution accepted an unfinished quote")
	}
}

func TestScanArgumentsRetainsUnfinishedFieldAndByteRanges(t *testing.T) {
	args := "\t\"发布 审查\"\t topic='two "
	fields, err := ScanArguments(args)
	if err == nil || len(fields) != 2 {
		t.Fatalf("ScanArguments = %+v, %v", fields, err)
	}
	if fields[0].Value != "发布 审查" || args[fields[0].Start:fields[0].End] != `"发布 审查"` {
		t.Fatalf("quoted name range = %+v", fields[0])
	}
	if fields[1].Value != "topic=two " || args[fields[1].Start:fields[1].End] != "topic='two " || fields[1].End != len(args) {
		t.Fatalf("unfinished value range = %+v", fields[1])
	}
}

func TestQuoteArgumentProducesOneLiteralArgument(t *testing.T) {
	for _, value := range []string{"demo", "", "release review", `release "review"`, `release's "review"`, "发布 审查", "two\twords", `C:\work tree`} {
		quoted := QuoteArgument(value)
		got, err := SplitArguments(quoted)
		if err != nil || len(got) != 1 || got[0] != value {
			t.Errorf("QuoteArgument(%q) = %q, parsed as %q, %v", value, quoted, got, err)
		}
	}
	if got := QuoteArgument("release review"); got != `"release review"` {
		t.Errorf("readable quoted name = %q", got)
	}
}
