package logstash

import (
	"encoding/json"
	"github.com/fsouza/go-dockerclient"
	"github.com/gliderlabs/logspout/router"
	"github.com/stretchr/testify/assert"
	"net"
	"testing"
	"time"
)

var res string

type MockConn struct {
}

func (m MockConn) Close() error {
	return nil
}

func (m MockConn) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (m MockConn) Write(b []byte) (n int, err error) {
	res = string(b)
	return 0, nil
}

func (m MockConn) LocalAddr() net.Addr {
	return nil
}

func (m MockConn) RemoteAddr() net.Addr {
	return nil
}

func (m MockConn) SetDeadline(t time.Time) error {
	return nil
}

func (m MockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m MockConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestStreamNotJson(t *testing.T) {
	assert := assert.New(t)

	conn := MockConn{}

	adapter := newLogstashAdapter(new(router.Route), conn)

	assert.NotNil(adapter)

	logstream := make(chan *router.Message)
	container := makeDummyContainer()

	messages := []router.Message{
		makeDummyMessage(&container, "Line1"),
		makeDummyMessage(&container, "   Line1.1"),
		router.Message{Container: &container}, // need last message to trigger buffer flush
	}

	go func() {
		for _, msg := range messages {
			msg := msg // $%!^& golang
			logstream <- &msg
		}
		close(logstream)
	}()

	for i := 0; i < len(messages); i++ {
		adapter.Stream(logstream)
	}

	var data map[string]interface{}
	err := json.Unmarshal([]byte(res), &data)
	assert.Nil(err)

	assert.Equal("Line1\n   Line1.1", data["message"])

	var dockerInfo map[string]interface{}
	dockerInfo = data["docker"].(map[string]interface{})
	assert.Equal("name", dockerInfo["name"])
	assert.Equal("ID", dockerInfo["id"])
	assert.Equal("image", dockerInfo["image"])
	assert.Equal("hostname", dockerInfo["hostname"])
}

func TestStreamJson(t *testing.T) {
	assert := assert.New(t)
	conn := MockConn{}
	adapter := newLogstashAdapter(new(router.Route), conn)
	assert.NotNil(adapter)
	logstream := make(chan *router.Message)
	container := makeDummyContainer()

	str := `{ "remote_user": "-", "body_bytes_sent": "25", "request_time": "0.821", "status": "200", "request_method": "POST", "http_referrer": "-", "http_user_agent": "-" }`

	message := makeDummyMessage(&container, str)

	go func() {
		logstream <- &message
		logstream <- &router.Message{Container: &container}
		close(logstream)
	}()

	adapter.Stream(logstream)
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
