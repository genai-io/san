package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/tool"
)

func TestMixedProseWrapsNearRightEdge(t *testing.T) {
	const lead = "终端使用说明"
	const text = lead + "：启动后请检查窗口宽度，程序会在处理请求时显示 On it，完成后会在消息区域显示结果并等待下一条输入。你也可以调整终端窗口大小来观察多行文字如何在显示区域中排布。"
	const width = 120
	p := NewQuestionPrompt()
	p.Show(&tool.QuestionRequest{Questions: []tool.Question{{
		Header: "Choose", Question: text, Options: []tool.QuestionOption{{Label: "One", Description: text}},
	}}}, width)
	for _, tt := range []struct{ name, out string }{
		{"recap", RenderRecap("※", "", text, width)},
		{"user", RenderUserMessage(text, "", nil, nil, width)},
		{"side question", RenderSideAnswer(text, "OK", width, nil)},
		{"question modal", p.Render()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var started bool
			for line := range strings.SplitSeq(ansi.Strip(tt.out), "\n") {
				w := ansi.StringWidth(line)
				if w > width {
					t.Errorf("line width %d exceeds %d: %q", w, width, line)
				}
				if strings.Contains(line, lead) {
					started = true
					if w < width-6 || !strings.Contains(line, "On it") {
						t.Errorf("prose wrapped early at %d columns: %q", w, line)
					}
				}
			}
			if !started {
				t.Fatal("no rendered body")
			}
		})
	}
}

func TestWrapProseKeepsEnglishIdentifierIntact(t *testing.T) {
	if got := WrapProse("中文SanRenderer，继续", 14); got != "中文\nSanRenderer，\n继续" {
		t.Fatalf("wrapped prose = %q", got)
	}
}

func TestMarkdownWrapsKanaQuoteAndHangulList(t *testing.T) {
	for _, tt := range []struct{ marker, word, continuation string }{
		{"> ", "カタカナ", "│ "},
		{"- ", "가나다라", "  "},
	} {
		content := tt.marker + strings.Repeat(tt.word, 6) + " On it" + strings.Repeat(tt.word, 12)
		out, err := NewMDRenderer(120).Render(content)
		if err != nil {
			t.Fatal(err)
		}
		lines := nonEmptyLines(ansi.Strip(out))
		if len(lines) < 2 || !strings.Contains(lines[0], "On it") || !strings.HasPrefix(lines[1], tt.continuation) {
			t.Fatalf("did not preserve %q block or phrase when wrapping: %q", tt.marker, lines)
		}
	}
}
