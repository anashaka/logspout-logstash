package logstash

import (
	"encoding/json"
	"errors"
	_ "expvar"
	"log"
	"net"
	"regexp"
	"strconv"
	"time"

	"github.com/gliderlabs/logspout/router"
	"github.com/rcrowley/go-metrics"
	"github.com/rcrowley/go-metrics/exp"
	"github.com/udacity/logspout-logstash/multiline"
	"strings"
	"fmt"
)

var (
	logMeter = metrics.NewMeter()
)

func init() {
	router.AdapterFactories.Register(NewLogstashAdapter, "logstash")
	exp.Exp(metrics.DefaultRegistry)
	metrics.Register("logstash_message_rate", logMeter)
}

type newMultilineBufferFn func() (multiline.MultiLine, error)

// LogstashAdapter is an adapter that streams TCP JSON to Logstash.
type LogstashAdapter struct {
	write            writer
	route            *router.Route
	cache            map[string]*multiline.MultiLine
	cacheTTL         time.Duration
	cachedLines      metrics.Gauge
	mkBuffer         newMultilineBufferFn
	cleanupRegExp    *regexp.Regexp
	javaLogRegExp    *regexp.Regexp
	staskTraceRegExp *regexp.Regexp
	causeRegExp      *regexp.Regexp
}

type ControlCode int

const (
	Continue ControlCode = iota
	Quit
)

func newLogstashAdapter(route *router.Route, write writer) *LogstashAdapter {
	patternString, ok := route.Options["pattern"]
	if !ok {
		patternString = `(^\s)|(^Caused by:)`
	}

	groupWith, ok := route.Options["group_with"]
	if !ok {
		groupWith = "previous"
	}

	negate := false
	negateStr, _ := route.Options["negate"]
	if negateStr == "true" {
		negate = true
	}

	separator, ok := route.Options["separator"]
	if !ok {
		separator = "\n"
	}

	maxLines, err := strconv.Atoi(route.Options["max_lines"])
	if err != nil {
		maxLines = 0
	}

	cacheTTL, err := time.ParseDuration(route.Options["cache_ttl"])
	if err != nil {
		cacheTTL = 10 * time.Second
	}

	cleanupPattern, ok := route.Options["cleanup_pattern"]
	if !ok {
		cleanupPattern = `\033\[[0-9;]*?m`
	}

	javaLogPattern, ok := route.Options["java_pattern"]
	if !ok {
		javaLogPattern = `([\d:.]+?)\[(\w+?)\s*?\]\[(.*?)\]\[(.*?)\](.*?)\s*?:([\S\w\W]*?)$`
	}

	stacktracePattern, ok := route.Options["stacktrace_pattern"]
	if !ok {
		stacktracePattern = `at (?P<fullclass>com\.mm.+?)\.(?P<method>[\w]+)\((?P<classLine>[\w\.]+:[\d]+)\)\s\~?\[(?P<file>.*)\]`
	}

	causePattern, ok := route.Options["cause_pattern"]
	if !ok {
		causePattern = `^(.*?):\s(.*)`
	}

	cleanupRegExp := regexp.MustCompile(cleanupPattern)
	javaLogRegExp := regexp.MustCompile(javaLogPattern)
	staskTraceRegExp := regexp.MustCompile(stacktracePattern)
	causeRegExp := regexp.MustCompile(causePattern)

	cachedLines := metrics.NewGauge()
	metrics.Register(route.ID + "_cached_lines", cachedLines)

	return &LogstashAdapter{
		route:       route,
		write:       write,
		cache:       make(map[string]*multiline.MultiLine),
		cacheTTL:    cacheTTL,
		cachedLines: cachedLines,
		mkBuffer: func() (multiline.MultiLine, error) {
			return multiline.NewMultiLine(
				&multiline.MultilineConfig{
					Pattern:   regexp.MustCompile(patternString),
					GroupWith: groupWith,
					Negate:    negate,
					Separator: &separator,
					MaxLines:  maxLines,
				})
		},
		cleanupRegExp : cleanupRegExp,
		javaLogRegExp : javaLogRegExp,
		staskTraceRegExp : staskTraceRegExp,
		causeRegExp : causeRegExp,
	}
}

// NewLogstashAdapter creates a LogstashAdapter with TCP as the default transport.
func NewLogstashAdapter(route *router.Route) (router.LogAdapter, error) {
	transportId, ok := route.Options["transport"]
	if !ok {
		transportId = "udp"
	}

	transport, found := router.AdapterTransports.Lookup(route.AdapterTransport(transportId))
	if !found {
		return nil, errors.New("unable to find adapter: " + route.Adapter)
	}

	conn, err := transport.Dial(route.Address, route.Options)
	if err != nil {
		return nil, err
	}

	var write writer
	if transportId == "tcp" {
		write = tcpWriter(conn)
	} else {
		write = defaultWriter(conn)
	}

	return newLogstashAdapter(route, write), nil
}

func (a *LogstashAdapter) lookupBuffer(msg *router.Message) *multiline.MultiLine {
	key := msg.Container.ID + msg.Source
	if a.cache[key] == nil {
		ml, _ := a.mkBuffer()
		a.cache[key] = &ml
	}
	return a.cache[key]
}

// Stream implements the router.LogAdapter interface.
func (a *LogstashAdapter) Stream(logstream chan *router.Message) {
	cacheTicker := time.NewTicker(a.cacheTTL).C

	for {
		msgs, ccode := a.readMessages(logstream, cacheTicker)
		a.sendMessages(msgs)

		switch ccode {
		case Continue:
			continue
		case Quit:
			return
		}
	}
}

func (a *LogstashAdapter) readMessages(
logstream chan *router.Message,
cacheTicker <-chan time.Time) ([]*router.Message, ControlCode) {
	select {
	case t := <-cacheTicker:
		return a.expireCache(t), Continue
	case msg, ok := <-logstream:
		if ok {
			return a.bufferMessage(msg), Continue
		} else {
			return a.flushPendingMessages(), Quit
		}
	}
}

func (a *LogstashAdapter) bufferMessage(msg *router.Message) []*router.Message {
	msgOrNil := a.lookupBuffer(msg).Buffer(msg)

	if msgOrNil == nil {
		return []*router.Message{}
	} else {
		return []*router.Message{msgOrNil}
	}
}

func (a *LogstashAdapter) expireCache(t time.Time) []*router.Message {
	var messages []*router.Message
	var linesCounter int64 = 0

	for id, buf := range a.cache {
		linesCounter += int64(buf.PendingSize())
		msg := buf.Expire(t, a.cacheTTL)
		if msg != nil {
			messages = append(messages, msg)
			delete(a.cache, id)
		}
	}

	a.cachedLines.Update(linesCounter)

	return messages
}

func (a *LogstashAdapter) flushPendingMessages() []*router.Message {
	var messages []*router.Message

	for _, buf := range a.cache {
		msg := buf.Flush()
		if msg != nil {
			messages = append(messages, msg)
		}
	}

	return messages
}

func (a *LogstashAdapter) sendMessages(msgs []*router.Message) {
	for _, msg := range msgs {
		if err := a.sendMessage(msg); err != nil {
			log.Fatal("logstash:", err)
		}
	}
	logMeter.Mark(int64(len(msgs)))
}

func (a *LogstashAdapter) sendMessage(msg *router.Message) error {
	buff, err := a.serialize(msg)

	if err != nil {
		return err
	}
	_, err = a.write(buff)
	if err != nil {
		return err
	}

	return nil
}

func (a *LogstashAdapter) serialize(msg *router.Message) ([]byte, error) {
	var js []byte
	var jsonMsg map[string]interface{}

	dockerInfo := DockerInfo{
		Name:     msg.Container.Name,
		ID:       msg.Container.ID,
		Image:    msg.Container.Config.Image,
		Hostname: msg.Container.Config.Hostname,
	}
	componentInfo := ComponentInfo{
		Name:    msg.Container.Config.Labels["com.docker.compose.service"],
		Version: msg.Container.Config.Labels["com.mm.version"],
		Env:     msg.Container.Config.Labels["com.mm.env"],
	}

	javaLog, parsedMsg := a.parseJavaMsg(&msg.Data)
	err := json.Unmarshal([]byte(msg.Data), &jsonMsg)
	if err != nil {
		// the message is not in JSON make a new JSON message
		msgToSend := LogstashMessage{
			Message: *parsedMsg,
			Docker:  dockerInfo,
			Component: componentInfo,
			Stream:  msg.Source,
			JavaLog: javaLog,
		}
		js, err = json.Marshal(msgToSend)
		if err != nil {
			return nil, err
		}

	} else {
		// the message is already in JSON just add the docker specific fields as a nested structure
		jsonMsg["docker"] = dockerInfo
		if (javaLog != nil) {
			jsonMsg["javaLog"] = javaLog
		}
		jsonMsg["component"] = componentInfo
		jsonMsg["message"] = *parsedMsg
		js, err = json.Marshal(jsonMsg)
		if err != nil {
			return nil, err
		}
	}

	return js, nil
}

func (a *LogstashAdapter) parseJavaMsg(msg *string) (*JavaLog, *string) {
	var cleanMsg = a.cleanupRegExp.ReplaceAllLiteralString(*msg, "")
	match := a.javaLogRegExp.FindStringSubmatch(cleanMsg)
	if (match == nil) {
		return nil, msg
	}
	exception := a.parseJavaException(&match[6])

	javaLog := JavaLog{
		Timestamp:  match[1],
		Level: match[2],
		Uuid: match[3],
		Thread: match[4],
		Logger: match[5],
		Exception: exception,
	}
	fmt.Println(javaLog)
	result := strings.Trim(match[6], " \t\n\r")
	return &javaLog, &result
}

func (a *LogstashAdapter) parseJavaException(javaMsg *string) *JavaException {
	if (strings.Contains(*javaMsg, "at ")) {
		splitByCause := strings.Split(*javaMsg, "Caused by: ")
		for i := len(splitByCause) - 1; i >= 0; i -= 1 {
			cause := splitByCause[i]
			stackMatch := a.staskTraceRegExp.FindStringSubmatch(cause)
			if (stackMatch == nil) {
				continue
			}
			causeMatch := a.causeRegExp.FindStringSubmatch(cause)
			if (len(causeMatch) == 3 && len(stackMatch) == 5) {
				javaException := JavaException{
					CauseException : causeMatch[1],
					CauseMessage: causeMatch[2],
					FullClass : stackMatch[1],
					Method : stackMatch[2],
					ClassLine : stackMatch[3],
					Jar : stackMatch[4],
				}
				fmt.Println(javaException)
				return &javaException
			}
		}
	}
	return nil
}

type DockerInfo struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Image    string `json:"image"`
	Hostname string `json:"hostname"`
}

type ComponentInfo struct {
	Name    string `json:"name"`
	Env     string `json:"env"`
	Version string `json:"version"`
}

type JavaLog struct {
	Timestamp string  `json:"timestamp"`
	Level     string  `json:"level"`
	Uuid      string  `json:"uuid"`
	Thread    string `json:"thread"`
	Logger    string `json:"logger"`
	Exception *JavaException `json:"exception,omitempty"`
}

type JavaException struct {
	CauseException string `json:"causeEx"`
	CauseMessage   string `json:"causeMsg"`
	FullClass      string `json:"fullclass"`
	Method         string `json:"method"`
	ClassLine      string `json:"classline"`
	Jar            string `json:"jar"`
}

// LogstashMessage is a simple JSON input to Logstash.
type LogstashMessage struct {
	Message   string      `json:"message"`
	Stream    string      `json:"stream"`
	Docker    DockerInfo  `json:"docker"`
	Component ComponentInfo `json:"component"`
	JavaLog   *JavaLog `json:"javaLog,omitempty"`
}

// writers
type writer func(b []byte) (int, error)

func defaultWriter(conn net.Conn) writer {
	return func(b []byte) (int, error) {
		return conn.Write(b)
	}
}

func tcpWriter(conn net.Conn) writer {
	return func(b []byte) (int, error) {
		// append a newline
		return conn.Write([]byte(string(b) + "\n"))
	}
}


