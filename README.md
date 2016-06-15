# logspout-logstash
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
