// +build !integration

package multiline

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gliderlabs/logspout/router"
	"github.com/stretchr/testify/assert"
)

func TestMultilinePreviousOK(t *testing.T) {
	testMultilineOK(t,
		MultilineConfig{
			Pattern:   regexp.MustCompile(`^\s`), // next line is indented by spaces
			GroupWith: "previous",
		},
		"line1\n  line1.1\n  line1.2\n",
		"line2\n  line2.1\n  line2.2\n",
	)
}

func TestMultilineJavaStackTraceOK(t *testing.T) {
	testMultilineOK(t,
		MultilineConfig{
			Pattern:   regexp.MustCompile(`(^\s)|(^Caused by:)`),
			GroupWith: "previous",
		},
		"line1\n  line1.1\n  line1.2\n",
		`javax.servlet.ServletException: Something bad happened
    at com.example.myproject.OpenSessionInViewFilter.doFilter(OpenSessionInViewFilter.java:60)
    at org.mortbay.jetty.servlet.ServletHandler$CachedChain.doFilter(ServletHandler.java:1157)
Caused by: com.example.myproject.MyProjectServletException
    at javax.servlet.http.HttpServlet.service(HttpServlet.java:727)
    at javax.servlet.http.HttpServlet.service(HttpServlet.java:820)
    ... 27 more`,
	)
}

// (^d+serror)|(^.+Exception: .+)|(^s+at .+)|(^s+... d+ more)|(^s*Caused by:.+)

func TestMultilinePreviousMidGroupOK(t *testing.T) {
	testMultilineOK(t,
		MultilineConfig{
			Pattern:   regexp.MustCompile(`^\s`), // next line is indented by spaces
			GroupWith: "previous",
		},
		"   line1.1\n  line1.2\n", // stream starts in middle of a group
		"line2\n  line2.1\n  line2.2\n",
	)
}

func TestMultilineNextOK(t *testing.T) {
	testMultilineOK(t,
		MultilineConfig{
			Pattern:   regexp.MustCompile(`\\$`), // previous line ends with \
			GroupWith: "next",
		},
		"line1 \\\nline1.1 \\\nline1.2\n",
		"line2 \\\nline2.1 \\\nline2.2\n",
	)
}

func TestMultilinePreviousNegateOK(t *testing.T) {
	testMultilineOK(t,
		MultilineConfig{
			Pattern:   regexp.MustCompile(`^-`), // first line starts with '-' at beginning of line
			Negate:    true,
			GroupWith: "previous",
		},
		"-line1\n  - line1.1\n  - line1.2\n",
		"-line2\n  - line2.1\n  - line2.2\n",
	)
}

func TestMultilineNextNegateOK(t *testing.T) {
	testMultilineOK(t,
		MultilineConfig{
			Pattern:   regexp.MustCompile(`;$`), // last line ends with ';'
			Negate:    true,
			GroupWith: "next",
		},
		"line1\nline1.1\nline1.2;\n",
		"line2\nline2.1\nline2.2;\n",
	)
}

func TestMultilineMaxLinesExceededOk(t *testing.T) {
	input := []string{
		"line1\n  line1.1\n  line1.2\n",
		"line2\n  line2.1\n  line2.2\n",
	}
	expected := []string{
		"line1\n  line1.1\n[Truncated]",
		"line2\n  line2.1\n[Truncated]",
	}
	ml, _ := NewMultiLine(&MultilineConfig{
		Pattern:   regexp.MustCompile(`^\s`), // next line is indented by spaces
		GroupWith: "previous",
		MaxLines:  2,
	})

	ml, lines := exercise(ml, input...)
	checkOutput(t, expected, lines)
}

func testMultilineOK(t *testing.T, cfg MultilineConfig, expected ...string) {
	var lines []*router.Message

	ml, err := NewMultiLine(&cfg)
	if err != nil {
		t.Fatalf("failed to initializ Multiline: %v", err)
	}

	ml, lines = exercise(ml, expected...)

	checkOutput(t, expected, lines)
}

func exercise(ml MultiLine, logInput ...string) (MultiLine, []*router.Message) {
	var lines []*router.Message

	for _, line := range createLines(logInput...) {
		ml = Step(ml, line)
		if ml.State == Flushed {
			lines = append(lines, ml.Last)
		}
	}

	ml = flushPendingLine(ml, &lines)

	return ml, lines
}

func checkOutput(t *testing.T, expected []string, output []*router.Message) {
	for i, expected := range expected {
		actual := output[i]
		var tsZero time.Time
		assert.NotEqual(t, tsZero, actual.Time)
		assert.Equal(t, trimTrailing(expected), trimTrailing(string(actual.Data)))
	}
}

func flushPendingLine(ml MultiLine, lines *[]*router.Message) MultiLine {
	if len(ml.pending) > 0 && ml.pending[0].Data != "" {
		ml = Flush(ml, &router.Message{Data: ""})
		*lines = append(*lines, ml.Last)
	}
	return ml
}

func trimTrailing(s string) string {
	return strings.TrimRight(s, "\r\n ")
}

func createLines(lineData ...string) []*router.Message {
	var lines []*router.Message
	for _, text := range strings.Split(strings.Join(lineData, ""), "\n") {
		lines = append(lines, &router.Message{
			Data: text,
			Time: time.Now(),
		})
	}
	return lines
}
