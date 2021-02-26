package pinger

import (
	"errors"
	"net"
	"sync"
	"time"
)

type key struct {
	ip  [4]byte
	seq int
}

type entry struct {
	timer *time.Timer
	done  chan<- struct{}
}

type timeoutTable struct {
	mux         sync.Mutex
	m           map[key]entry
	timeout     time.Duration
	handlerFunc timeoutHandlerFunc
}

type timeoutHandlerFunc func(ip net.IP, seq int)

func newTimeoutTable(timeout time.Duration,
	handler timeoutHandlerFunc) (*timeoutTable, error) {
	if timeout <= 0 {
		return nil, errors.New("timeout must be greater than zero")
	}

	return &timeoutTable{
		timeout:     timeout,
		handlerFunc: handler,
		m:           make(map[key]entry, 512),
	}, nil
}

func (tt *timeoutTable) add(ip net.IP, seq int) error {
	done := make(chan struct{})
	key := makeKey(ip, seq)
	timer := time.NewTimer(tt.timeout)
	entry := entry{timer, done}

	tt.mux.Lock()
	tt.m[key] = entry
	tt.mux.Unlock()

	go func(done <-chan struct{}) {
		select {
		case <-timer.C: // The retrieval timed out
			tt.mux.Lock()
			delete(tt.m, key)
			tt.mux.Unlock()

			if tt.handlerFunc != nil {
				tt.handlerFunc(ip, seq)
			}
		case <-done: // The item was retrieved and timeout-processing is not req'd
			return
		}
	}(done)

	return nil
}

func (tt *timeoutTable) remove(ip net.IP, seq int) bool {
	key := makeKey(ip, seq)
	return tt.removeKey(key)
}

func (tt *timeoutTable) removeKey(key key) bool {
	tt.mux.Lock()
	entry, ok := tt.m[key]
	if !ok {
		tt.mux.Unlock()
		return false
	}

	delete(tt.m, key)
	tt.mux.Unlock()

	// Cancel timeout-processing goroutine
	close(entry.done)

	// Cancel timeout
	if !entry.timer.Stop() {
		<-entry.timer.C
	}

	return true
}

func (tt *timeoutTable) cleardown() {
	for key := range tt.m {
		tt.removeKey(key)
	}
}

func makeKey(ip net.IP, seq int) key {
	ipv4 := ip.To4()
	ipv4Array := [4]byte{ipv4[0], ipv4[1], ipv4[2], ipv4[3]}
	key := key{ipv4Array, seq}
	return key
}
