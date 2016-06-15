package multiline

import (
	"fmt"
	"github.com/gliderlabs/logspout/router"
	"regexp"
	"strings"
)

type MultilineConfig struct {
	Negate    bool           `config:"negate"`
	GroupWith string         `config:"match"       validate:"required"`
	Separator *string        `config:"separator"`
	MaxLines  int            `config:"max_lines"`
	Pattern   *regexp.Regexp `config:"pattern"`
}

// MultiLine processor combining multiple line events into one multi-line event.
//
// Lines to be combined are matched by some configurable predicate using
// regular expression.
//
// The maximum number of lines to be returned is fully configurable.
// Even if limits are reached subsequent lines are matched, until event is
// fully finished.
type MultiLine struct {
	isMultiline matcher
	maxLines    int
	separator   string

	pending []*router.Message
	Last    *router.Message
	State   MLState
}

type MLState int

const (
	Buffering MLState = iota
	Flushed
)

const (
	// Default maximum number of lines to return in one multi-line event
	defaultMaxLines = 1 << 10

	defaultSeparator = "\n"
)

// Matcher represents the predicate comparing any two lines
// to find start and end of multiline events in stream of line events.
type matcher func(lastText, currentText string) bool

// NewMultiLine creates a new multi-line processor combining stream of
// line events into stream of multi-line events.
func NewMultiLine(config *MultilineConfig) (MultiLine, error) {
	types := map[string]func(*regexp.Regexp) (matcher, error){
		"next":     nextMatcher,
		"previous": previousMatcher,
	}

	matcherType, ok := types[config.GroupWith]
	if !ok {
		return MultiLine{}, fmt.Errorf("unknown matcher type: %s", config.GroupWith)
	}

	matcher, err := matcherType(config.Pattern)
	if err != nil {
		return MultiLine{}, err
	}

	if config.Negate {
		matcher = negatedMatcher(matcher)
	}

	maxLines := defaultMaxLines
	if config.MaxLines > 0 {
		maxLines = config.MaxLines
	}

	separator := defaultSeparator
	if config.Separator != nil {
		separator = *config.Separator
	}

	ml := MultiLine{
		isMultiline: matcher,
		separator:   separator,
		maxLines:    maxLines,
	}
	return ml, nil
}

// Step returns the next multi-line value
func Step(ml MultiLine, next *router.Message) MultiLine {
	if len(ml.pending) == 0 {
		return addPending(ml, next)
	} else if ml.isMultiline(getLastPendingData(&ml), next.Data) {
		return addPending(ml, next)
	} else {
		return Flush(ml, next)
	}
}

func Flush(ml MultiLine, next *router.Message) MultiLine {
	var buffer []string

	for _, message := range ml.pending {
		buffer = append(buffer, message.Data)
	}

	ml.Last = ml.pending[0]
	ml.Last.Data = strings.Join(buffer, ml.separator)
	ml.pending = []*router.Message{next}
	ml.State = Flushed

	return ml
}

func getLastPendingData(ml *MultiLine) string {
	return ml.pending[len(ml.pending)-1].Data
}

func addPending(ml MultiLine, next *router.Message) MultiLine {
	if len(ml.pending) < ml.maxLines {
		ml.pending = append(ml.pending, next)
	} else if len(ml.pending) == ml.maxLines {
		truncMessage := *next
		truncMessage.Data = "[Truncated]"
		ml.pending = append(ml.pending, &truncMessage)
	}
	ml.Last = nil
	ml.State = Buffering
	return ml
}

// matchers

func previousMatcher(regex *regexp.Regexp) (matcher, error) {
	return genPatternMatcher(regex, func(lastText, currentText string) string {
		return currentText
	})
}

func nextMatcher(regex *regexp.Regexp) (matcher, error) {
	return genPatternMatcher(regex, func(lastText, currentText string) string {
		return lastText
	})
}

func negatedMatcher(m matcher) matcher {
	return func(lastText, currentText string) bool {
		return !m(lastText, currentText)
	}
}

func genPatternMatcher(
	regex *regexp.Regexp,
	sel func(lastText, currentText string) string,
) (matcher, error) {
	matcher := func(lastText, currentText string) bool {
		line := sel(lastText, currentText)
		return regex.MatchString(line)
	}
	return matcher, nil
}
