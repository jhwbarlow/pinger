package pinger

import (
	"errors"
	"log"
	"net"
	"testing"
	"time"
)

func TestPingSuccess(t *testing.T) {
	replied, timedOut, spurious, err := test(false, false, false, false)
	switch {
	case replied:
	case timedOut:
		t.Errorf("got timeout, expected reply")
	case spurious:
		t.Errorf("got spurious message, expected timeout")
	case err != nil:
		t.Errorf("got error %v, expected nil", err)
	}
}

func TestPingTimeout(t *testing.T) {
	replied, timedOut, spurious, err := test(true, false, false, false)
	switch {
	case timedOut:
	case replied:
		t.Errorf("got reply, expected timeout")
	case spurious:
		t.Errorf("got spurious message, expected timeout")
	case err != nil:
		t.Errorf("got error %v, expected none", err)
	}
}

func TestWriteErrorSendsErrorOnChan(t *testing.T) {
	replied, timedOut, spurious, err := test(false, true, false, false)
	switch {
	case errors.Is(err, errWrite):
		t.Logf("got error: %v (of type %T)", err, err)
	case err != nil:
		t.Errorf("got error of type %T, expected %T", err, errWrite)
	case timedOut:
		t.Errorf("got timeout, expected error")
	case replied:
		t.Errorf("got reply, expected error")
	case spurious:
		t.Errorf("got spurious message, expected timeout")
	}
}

func TestReadErrorSendsErrorOnChan(t *testing.T) {
	replied, timedOut, spurious, err := test(false, false, true, false)
	switch {
	case errors.Is(err, errRead):
		t.Logf("got error: %v (of type %T)", err, err)
	case err != nil:
		t.Errorf("got error of type %T, expected %T", err, errWrite)
	case timedOut:
		t.Errorf("got timeout, expected error")
	case replied:
		t.Errorf("got reply, expected error")
	case spurious:
		t.Errorf("got spurious message, expected timeout")
	}
}

func TestSpurious(t *testing.T) {
	replied, timedOut, spurious, err := test(false, false, false, true)
	switch {
	case spurious:
	case err != nil:
		t.Errorf("got error %v, expected none", err)
	case timedOut:
		t.Errorf("got timeout, expected spurious message handler func callback")
	case replied:
		t.Errorf("got reply, expected spurious message handler func callback")
	}
}

func test(dontReply, returnErrOnWrite, returnErrOnRead, returnSpurious bool) (replied, timedOut, spurious bool, err error) {
	replyDone := make(chan struct{})
	replyHandler := func(_ net.IP, _ int) {
		replyDone <- struct{}{}
	}

	timeoutDone := make(chan struct{})
	timeoutHandler := func(_ net.IP, _ int) {
		timeoutDone <- struct{}{}
	}

	spuriousDone := make(chan struct{})
	spuriousHandler := func(_ net.IP) {
		spuriousDone <- struct{}{}
	}

	pinger, err := New(1*time.Millisecond, 1*time.Millisecond, []byte("test-ping"))
	if err != nil {
		log.Fatalf("Pinger construction error: %v", err)
	}
	pinger.WithSuccessHandler(replyHandler).
		WithTimeoutHandler(timeoutHandler).
		WithSpuriousHandler(spuriousHandler)

	pinger.pingConnCreateFunc = func() (pingConn, error) {
		return &mockPingConn{
			replyChan:        make(chan reply),
			dontReply:        dontReply,
			returnErrOnWrite: returnErrOnWrite,
			returnErrOnRead:  returnErrOnRead,
			returnSpurious:   returnSpurious,
			closed:           make(chan struct{}),
		}, nil
	}

	if err := pinger.Init(); err != nil {
		log.Fatalf("Pinger init error: %v", err)
	}

	pinger.AddPeer("192.168.1.1")

	done := make(chan struct{})
	defer close(done)
	errChan := pinger.Run(done)

	select {
	case <-timeoutDone:
		return false, true, false, nil
	case <-replyDone:
		return true, false, false, nil
	case <-spuriousDone:
		return false, false, true, nil
	case err := <-errChan:
		return false, false, false, err
	}
}
