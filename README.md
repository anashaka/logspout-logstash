# logspout-logstash
[![CircleCI](https://circleci.com/gh/udacity/logspout-logstash.svg?style=svg)](https://circleci.com/gh/udacity/logspout-logstash)
[![Coverage Status](https://coveralls.io/repos/github/udacity/logspout-logstash/badge.svg?branch=master)](https://coveralls.io/github/udacity/logspout-logstash?branch=master)

A minimalistic adapter for github.com/gliderlabs/logspout to write to Logstash TCP.  Supports

* multi-line log grouping
* udacity metadata

Log lines identified as JSON preserve the app-specific fields when shipped to Logstash.

Follow the instructions in https://github.com/gliderlabs/logspout/tree/master/custom on how to build your own Logspout container with custom modules. Basically just copy the contents of the custom folder and include:

```
import (
  _ "github.com/udacity/logspout-logstash"
  _ "github.com/gliderlabs/logspout/transports/udp"
)
```

in modules.go.

Use by setting `ROUTE_URIS=logstash://host:port` to the Logstash host and port for TCP.

In your logstash config, set the input codec to `json` e.g:

```
input {
  tcp {
    port => 5000
    codec => json
  }
}
```

## Developing

```
### See Makefile
### All build artifacts have make-style dependencies defined

# install build dependencies
make deps

# test
make test

# view coverage report in your browser
make view-coverage

# submit a coveralls report
make coveralls
```
