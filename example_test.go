package pinger_test

import (
	"log"
	"net"
	"time"

	"github.com/jhwbarlow/pinger"
)

func Example() {
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
