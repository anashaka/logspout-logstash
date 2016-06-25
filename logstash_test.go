package logstash

import (
	"encoding/json"
	"github.com/fsouza/go-dockerclient"
	"github.com/gliderlabs/logspout/router"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
	"time"
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
