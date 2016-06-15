package logstash

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"regexp"
	"strconv"

	"github.com/gliderlabs/logspout/router"
	"github.com/udacity/logspout-logstash/multiline"
)

func init() {
	router.AdapterFactories.Register(NewLogstashAdapter, "logstash")
}

type newMultilineBufferFn func() (multiline.MultiLine, error)

// LogstashAdapter is an adapter that streams TCP JSON to Logstash.
type LogstashAdapter struct {
	conn     net.Conn
	route    *router.Route
	cache    map[string]*multiline.MultiLine
	mkBuffer newMultilineBufferFn
}

func newLogstashAdapter(route *router.Route, conn net.Conn) *LogstashAdapter {
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

	return &LogstashAdapter{
		route: route,
		conn:  conn,
		cache: make(map[string]*multiline.MultiLine),
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
	transport, found := router.AdapterTransports.Lookup(route.AdapterTransport("tcp"))
	if !found {
		return nil, errors.New("unable to find adapter: " + route.Adapter)
	}

	conn, err := transport.Dial(route.Address, route.Options)
	if err != nil {
		return nil, err
	}

	return newLogstashAdapter(route, conn), nil
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
	for m := range logstream {
		multiLineBuffer := a.lookupBuffer(m.Container.ID)
		*multiLineBuffer = multiline.Step(*multiLineBuffer, m)

		if multiLineBuffer.State == multiline.Flushed {
			err := a.writeMessage(multiLineBuffer.Last)
			if err != nil {
				log.Println("logstash:", err)
			}
		}
	}
}

func (a *LogstashAdapter) writeMessage(m *router.Message) error {
	buff, err := serialize(m)

	if err != nil {
		return err
	}
	_, err = a.conn.Write(buff)
	if err != nil {
		return err
	}
	return nil
}

func serialize(m *router.Message) ([]byte, error) {
	var js []byte
	var jsonMsg map[string]interface{}

	dockerInfo := DockerInfo{
		Name:     m.Container.Name,
		ID:       m.Container.ID,
		Image:    m.Container.Config.Image,
		Hostname: m.Container.Config.Hostname,
	}
	udacityInfo := UdacityInfo{
		Name:    m.Container.Config.Labels["com.udacity.name"],
		Env:     m.Container.Config.Labels["com.udacity.version"],
		Version: m.Container.Config.Labels["com.udacity.env"],
	}

	err := json.Unmarshal([]byte(m.Data), &jsonMsg)

	if err != nil {
		// the message is not in JSON make a new JSON message
		msg := LogstashMessage{
			Message: m.Data,
			Docker:  dockerInfo,
			Udacity: udacityInfo,
			Stream:  m.Source,
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
