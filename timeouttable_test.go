package pinger

import (
	"bytes"
	"net"
	"testing"
	"time"
)

var ip = net.IP{1, 1, 1, 1}

const seq int = 1

func TestStoreAndRetrieve(t *testing.T) {
	timeoutHandlerCalled := false

	timeoutHandlerFunc := func(_ net.IP, _ int) {
		timeoutHandlerCalled = true
	}

	table, err := newTimeoutTable(1*time.Minute, timeoutHandlerFunc)
	if err != nil {
		t.Errorf("expected nil error, got %v (of type %T)", err, err)
	}

	if err := table.add(ip, seq); err != nil {
		t.Errorf("expected nil error, got %q (of type %T)", err, err)
	}

	if ok := table.remove(ip, seq); !ok {
		t.Error("expected true removal, got false")
	}

	if timeoutHandlerCalled {
		t.Error("expected timeout handler to not be called, but was")
	}
}

func TestStoreAndTimeout(t *testing.T) {
	timeoutHandlerCalled := false
	var callbackIP net.IP = nil
	callbackSeq := -1

	timeoutHandlerFunc := func(ip net.IP, seq int) {
		timeoutHandlerCalled = true
		callbackIP = ip
		callbackSeq = seq
	}

	table, err := newTimeoutTable(10*time.Millisecond, timeoutHandlerFunc)
	if err != nil {
		t.Errorf("expected nil error, got %v (of type %T)", err, err)
	}

	if err := table.add(ip, seq); err != nil {
		t.Errorf("expected nil error, got %q (of type %T)", err, err)
	}

	// Wait long enough to ensure the table entry times out
	time.Sleep(20 * time.Millisecond)

	if !timeoutHandlerCalled {
		t.Error("expected timeout handler to be called, but was not")
	}

	if !bytes.Equal(callbackIP, ip) {
		t.Errorf("expected timeout handler IP of %v, got %v", ip, callbackIP)
	}

	if !bytes.Equal(callbackIP, ip) {
		t.Errorf("expected timeout handler seq of %d, got %d", seq, callbackSeq)
	}

	if ok := table.remove(ip, seq); ok {
		t.Error("expected false removal, got true")
	}
}
