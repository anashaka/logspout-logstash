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

var res string

func mockWriter(b []byte) (int, error) {
	res = string(b)
	return 0, nil
}

func TestStreamNotJson(t *testing.T) {
	assert := assert.New(t)

	adapter := newLogstashAdapter(new(router.Route), mockWriter)

	assert.Nil(adapter)

	logstream := make(chan *router.Message)
	container := makeDummyContainer()
	lines := []string{
		"Line1",
		"   Line1.1",
	}

	go func() {
		var messages []router.Message
		for _, line := range lines {
			messages = append(messages, makeDummyMessage(&container, line))
		}

		for _, msg := range messages {
			msg := msg // $%!^& golang
			logstream <- &msg
		}
		close(logstream)
	}()

	adapter.Stream(logstream)

	var data map[string]interface{}
	err := json.Unmarshal([]byte(res), &data)
	assert.Nil(err)

	assert.Equal(strings.Join(lines, "\n"), data["message"])

	var dockerInfo map[string]interface{}
	dockerInfo = data["docker"].(map[string]interface{})
	assert.Equal("name", dockerInfo["name"])
	assert.Equal("ID", dockerInfo["id"])
	assert.Equal("image", dockerInfo["image"])
	assert.Equal("hostname", dockerInfo["hostname"])
}

func TestStreamJson(t *testing.T) {
	assert := assert.New(t)
	adapter := newLogstashAdapter(new(router.Route), mockWriter)
	assert.NotNil(adapter)
	logstream := make(chan *router.Message)
	container := makeDummyContainer()

	str := `{ "remote_user": "-", "body_bytes_sent": "25", "request_time": "0.821", "status": "200", "request_method": "POST", "http_referrer": "-", "http_user_agent": "-" }`

	message := makeDummyMessage(&container, str)

	go func() {
		logstream <- &message
		close(logstream)
	}()

	adapter.Stream(logstream)

	var data map[string]interface{}
	err := json.Unmarshal([]byte(res), &data)
	assert.Nil(err)

	assert.Equal("-", data["remote_user"])
	assert.Equal("25", data["body_bytes_sent"])
	assert.Equal("0.821", data["request_time"])
	assert.Equal("200", data["status"])
	assert.Equal("POST", data["request_method"])
	assert.Equal("-", data["http_referrer"])
	assert.Equal("-", data["http_user_agent"])

	var dockerInfo map[string]interface{}
	dockerInfo = data["docker"].(map[string]interface{})
	assert.Equal("name", dockerInfo["name"])
	assert.Equal("ID", dockerInfo["id"])
	assert.Equal("image", dockerInfo["image"])
	assert.Equal("hostname", dockerInfo["hostname"])
}

func makeDummyContainer() docker.Container {
	containerConfig := docker.Config{}
	containerConfig.Image = "image"
	containerConfig.Hostname = "hostname"

	container := docker.Container{}
	container.Name = "name"
	container.ID = "ID"
	container.Config = &containerConfig

	return container
}

func makeDummyMessage(container *docker.Container, data string) router.Message {
	return router.Message{
		Container: container,
		Source:    "FOOOOO",
		Data:      data,
		Time:      time.Now(),
	}
}
