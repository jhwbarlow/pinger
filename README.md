pinger
======
Pinger is a Go package used to ping multiple IPv4 peers at an interval, with the ability to register callback functions to be called upon a successful ping or a timeout.


Example
-------

Ping 192.168.1.1, 8.8.8.8 and 4.4.4.4 every second, with a timeout of five seconds. Each time a successful ping occurs (i.e. an ICMP echo reply is received which matches an ICMP echo request that was sent by the Pinger), `good` is incremented. Each time a timeout occurs (i.e. a matching ICMP echo reply is not received within the timeout period), `bad` is incremented.

This example exits after ten seconds, or an error occurs during the pinging process, whichever happens first.

```go
package main

import (
	"log"
	"net"
	"time"

	"github.com/jhwbarlow/pinger"
)

func main() {
	var good, bad int

	incGood := func(_ net.IP, _ int) {
		good++
		log.Printf("good: %d", good)
	}

	incBad := func(_ net.IP, _ int) {
		bad++
		log.Printf("bad: %d", bad)
	}

	pinger, err := pinger.New(1*time.Second, 5*time.Second, []byte("hello-world-ping"))
	if err != nil {
		log.Fatalf("Pinger construction error: %v", err)
	}
	pinger.WithSuccessHandler(incGood).WithTimeoutHandler(incBad)

	if err := pinger.Init(); err != nil {
		log.Fatalf("Pinger init error: %v", err)
	}

	pinger.AddPeer("8.8.8.8")
	pinger.AddPeer("4.4.4.4")
	pinger.AddPeer("192.168.1.1")

	done := make(chan struct{})
	errChan := pinger.Run(done)

	select {
	case <-time.Tick(10 * time.Second):
		close(done)
	case err := <-errChan:
		log.Fatalf("Run error: %v", err)
	}
}
```

