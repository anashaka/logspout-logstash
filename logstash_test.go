package logstash

import (
	"encoding/json"
	"github.com/fsouza/go-dockerclient"
	"github.com/gliderlabs/logspout/router"
	_ "github.com/gliderlabs/logspout/transports/tcp"
	_ "github.com/gliderlabs/logspout/transports/udp"
	"github.com/stretchr/testify/assert"
	"net"
	"strings"
	"testing"
	"time"
	"fmt"
)

func makeMockWriter() (writer, *[]string) {
	var tmp []string
	results := &tmp
	return func(b []byte) (int, error) {
		*results = append(*results, string(b))
		return 0, nil
	}, results
}

func TestStreamMultiline(t *testing.T) {
	assert := assert.New(t)

	mockWriter, results := makeMockWriter()
	adapter := newLogstashAdapter(new(router.Route), mockWriter)

	assert.NotNil(adapter)

	logstream := make(chan *router.Message)
	container := makeDummyContainer("anid")
	lines := []string{
		"Line1",
		"   Line1.1",
	}

	go pump(logstream, &container, [][]string{lines})

	adapter.Stream(logstream)
	data := parseResult(assert, (*results)[0])

	assert.Equal(strings.Join(lines, "\n"), data["message"])
	assertDockerInfo(assert, &container, data["docker"])
}

func TestStreamMultilineStacktrace(t *testing.T) {
	assert := assert.New(t)

	mockWriter, results := makeMockWriter()
	adapter := newLogstashAdapter(new(router.Route), mockWriter)

	assert.NotNil(adapter)

	logstream := make(chan *router.Message)
	container := makeDummyContainer("anid")
	lines := []string{
		"12:55:46.650[WARN ][6d3b36a5-63b5-4262-9274-0183bed44960][qtp1162918744-18]o.e.j.s.ServletHandler                   :  org.springframework.web.util.NestedServletException: Request processing failed; nested exception is java.lang.IllegalArgumentException: Message test",
		"	at org.eclipse.jetty.util.thread.QueuedThreadPool$3.run(QueuedThreadPool.java:572) [jetty-util-9.3.0.v20150612.jar:9.3.0.v20150612]",
		"	at java.lang.Thread.run(Thread.java:745) [?:1.8.0_25]",
		"	at com.mm.first.ge.controller.BlackListController.blackListSync(BlackListController.java:26) ~[main/:?]",
		"Caused by: java.lang.IllegalArgumentException: Message test",
		"	at com.mm.blacklist.ge.controller.BlackListController.blackListSync(BlackListController.java:26) ~[main/:?]",
		"	at sun.reflect.NativeMethodAccessorImpl.invoke0(Native Method) ~[?:1.8.0_25]",


	}

	go pump(logstream, &container, [][]string{lines})

	adapter.Stream(logstream)
	data := parseResult(assert, (*results)[0])

	fmt.Println(data)
	assert.Equal(strings.Join(lines, "\n"), data["message"])
	assertDockerInfo(assert, &container, data["docker"])
}


func TestStreamJson(t *testing.T) {
	assert := assert.New(t)
	mockWriter, results := makeMockWriter()
	adapter := newLogstashAdapter(new(router.Route), mockWriter)
	assert.NotNil(adapter)
	logstream := make(chan *router.Message)
	container := makeDummyContainer("anid")

	rawLine := `{ "remote_user": "-",
                "body_bytes_sent": "25",
                "request_time": "0.821",
                "status": "200",
                "request_method": "POST",
                "http_referrer": "-",
                "http_user_agent": "-" }`

	go pump(logstream, &container, [][]string{[]string{rawLine}})

	adapter.Stream(logstream)
	data := parseResult(assert, (*results)[0])

	assert.Equal("-", data["remote_user"])
	assert.Equal("25", data["body_bytes_sent"])
	assert.Equal("0.821", data["request_time"])
	assert.Equal("200", data["status"])
	assert.Equal("POST", data["request_method"])
	assert.Equal("-", data["http_referrer"])
	assert.Equal("-", data["http_user_agent"])

	assertDockerInfo(assert, &container, data["docker"])
}

func TestStreamMultipleMixedMessages(t *testing.T) {
	assert := assert.New(t)

	mockWriter, results := makeMockWriter()
	adapter := newLogstashAdapter(new(router.Route), mockWriter)

	logstream := make(chan *router.Message)
	container := makeDummyContainer("anid")
	expected := [][]string{
		[]string{
			"Line1",
			"   Line1.1",
		},
		[]string{
			`{"message":"I am json"}`,
		},
	}

	go pump(logstream, &container, expected)

	adapter.Stream(logstream)

	// first message
	data := parseResult(assert, (*results)[0])
	assert.Equal(strings.Join(expected[0], "\n"), data["message"])

	// second message
	data = parseResult(assert, (*results)[1])
	assert.Equal("I am json", data["message"])
}

func TestCacheExpiration(t *testing.T) {
	assert := assert.New(t)

	mockWriter, results := makeMockWriter()
	var r router.Route
	r.Options = make(map[string]string)
	r.Options["cache_ttl"] = "5ms"
	adapter := newLogstashAdapter(&r, mockWriter)
	logstream := make(chan *router.Message)
	container := makeDummyContainer("anid")

	go func() {
		msg := makeDummyMessage(&container, "test")
		logstream <- &msg
	}()

	go adapter.Stream(logstream)

	time.Sleep(15 * time.Millisecond)

	assert.Equal(1, len(*results), "cache timer must fire to force message flush")
	data := parseResult(assert, (*results)[0])
	assert.Equal("test", data["message"])

	close(logstream)
}

func TestTCPInit(t *testing.T) {
	assert := assert.New(t)
	l, err := net.Listen("tcp", "localhost:0")
	assert.Nil(err)
	defer l.Close()

	var r router.Route
	r.Options = make(map[string]string)
	r.Options["transport"] = "tcp"
	r.Address = l.Addr().String()
	_, err = NewLogstashAdapter(&r)
	assert.Nil(err)
}

func TestUDPInit(t *testing.T) {
	assert := assert.New(t)
	var r router.Route
	r.Address = "localhost:0"
	_, err := NewLogstashAdapter(&r)
	assert.Nil(err)
}

func makeDummyContainer(id string) docker.Container {
	containerConfig := docker.Config{}
	containerConfig.Image = "image"
	containerConfig.Hostname = "hostname"

	container := docker.Container{}
	container.Name = "name"
	container.ID = id
	container.Config = &containerConfig

	return container
}

func pump(logstream chan *router.Message, container *docker.Container, structureLines [][]string) {
	for _, singleMessage := range structureLines {
		for _, line := range singleMessage {
			msg := makeDummyMessage(container, line)
			logstream <- &msg
		}
	}
	close(logstream)
}

func makeDummyMessage(container *docker.Container, data string) router.Message {
	return router.Message{
		Container: container,
		Source:    "FOOOOO",
		Data:      data,
		Time:      time.Now(),
	}
}

func parseResult(assert *assert.Assertions, serialized string) map[string]interface{} {
	var data map[string]interface{}
	err := json.Unmarshal([]byte(serialized), &data)
	assert.Nil(err)
	return data
}

func assertDockerInfo(assert *assert.Assertions, expected *docker.Container, actual interface{}) {
	var dockerInfo map[string]interface{}
	dockerInfo = actual.(map[string]interface{})
	assert.Equal(expected.Name, dockerInfo["name"])
	assert.Equal(expected.ID, dockerInfo["id"])
	assert.Equal(expected.Config.Image, dockerInfo["image"])
	assert.Equal(expected.Config.Hostname, dockerInfo["hostname"])
}
