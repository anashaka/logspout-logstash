package logstash

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"regexp"
	"strconv"
	"time"

	"github.com/gliderlabs/logspout/router"
	"github.com/udacity/logspout-logstash/multiline"
)

func init() {
	router.AdapterFactories.Register(NewLogstashAdapter, "logstash")
}

type newMultilineBufferFn func() (multiline.MultiLine, error)

// LogstashAdapter is an adapter that streams TCP JSON to Logstash.
type LogstashAdapter struct {
	write    writer
	route    *router.Route
	cache    map[string]*multiline.MultiLine
	cacheTTL time.Duration
	mkBuffer newMultilineBufferFn
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

	return &LogstashAdapter{
		route:    route,
		write:    write,
		cache:    make(map[string]*multiline.MultiLine),
		cacheTTL: cacheTTL,
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

func (a *LogstashAdapter) lookupBuffer(key string) *multiline.MultiLine {
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
	msgOrNil := a.lookupBuffer(msg.Container.ID).Buffer(msg)

	if msgOrNil == nil {
		return []*router.Message{}
	} else {
		return []*router.Message{msgOrNil}
	}
}

func (a *LogstashAdapter) expireCache(t time.Time) []*router.Message {
	var messages []*router.Message

	for id, buf := range a.cache {
		msg := buf.Expire(t, a.cacheTTL)
		if msg != nil {
			messages = append(messages, msg)
			delete(a.cache, id)
		}
	}

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
}

func (a *LogstashAdapter) sendMessage(msg *router.Message) error {
	buff, err := serialize(msg)

	if err != nil {
		return err
	}
	_, err = a.write(buff)
	if err != nil {
		return err
	}

	return nil
}

func serialize(msg *router.Message) ([]byte, error) {
	var js []byte
	var jsonMsg map[string]interface{}

	dockerInfo := DockerInfo{
		Name:     msg.Container.Name,
		ID:       msg.Container.ID,
		Image:    msg.Container.Config.Image,
		Hostname: msg.Container.Config.Hostname,
	}
	udacityInfo := UdacityInfo{
		Name:    msg.Container.Config.Labels["com.udacity.name"],
		Version: msg.Container.Config.Labels["com.udacity.version"],
		Env:     msg.Container.Config.Labels["com.udacity.env"],
	}

	err := json.Unmarshal([]byte(msg.Data), &jsonMsg)

	if err != nil {
		// the message is not in JSON make a new JSON message
		msg := LogstashMessage{
			Message: msg.Data,
			Docker:  dockerInfo,
			Udacity: udacityInfo,
			Stream:  msg.Source,
		}
		js, err = json.Marshal(msg)
		if err != nil {
			return nil, err
		}
	} else {
		// the message is already in JSON just add the docker specific fields as a nested structure
		jsonMsg["docker"] = dockerInfo
		jsonMsg["udacity"] = udacityInfo

		js, err = json.Marshal(jsonMsg)
		if err != nil {
			return nil, err
		}
	}

	return js, nil
}

type DockerInfo struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Image    string `json:"image"`
	Hostname string `json:"hostname"`
}

type UdacityInfo struct {
	Name    string `json:"name"`
	Env     string `json:"env"`
	Version string `json:"version"`
}

// LogstashMessage is a simple JSON input to Logstash.
type LogstashMessage struct {
	Message string      `json:"message"`
	Stream  string      `json:"stream"`
	Docker  DockerInfo  `json:"docker"`
	Udacity UdacityInfo `json:"udacity"`
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
