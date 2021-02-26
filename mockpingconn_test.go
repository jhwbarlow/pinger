package pinger

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

var (
	errClosedConn = errors.New("write to closed mock connection")
	errWrite      = errors.New("mock write error")
	errRead       = errors.New("mock read error")
)

type reply struct {
	data []byte
	peer net.Addr
}

type mockPingConn struct {
	replyChan                                                    chan reply
	dontReply, returnErrOnWrite, returnErrOnRead, returnSpurious bool
	closed                                                       chan struct{}
}

func (mpc *mockPingConn) WriteTo(buf []byte, peer net.Addr) (written int, err error) {
	select {
	case <-mpc.closed:
		return 0, errClosedConn
	default:
	}

	if mpc.returnErrOnWrite {
		return 0, errWrite
	}

	if mpc.dontReply {
		// Tell Pinger the write was successful, but do not create a response
		return len(buf), nil
	}

	// Parse Pinger's request. If parsing fails, it is a badly formatted ICMP echo
	// request message. Returning an error will cause an error to appear on the Pinger's
	// error channel, which can be used to fail the test.
	icmpReq, err := icmp.ParseMessage(icmpProtoNoIPv4, buf)
	if err != nil {
		return 0, fmt.Errorf("error parsing ICMP request from pinger: %w", err)
	}

	if icmpReq.Type != ipv4.ICMPTypeEcho {
		return 0, fmt.Errorf("error parsing ICMP echo request: unexpected ICMP type: %d", icmpReq.Type)
	}

	echoReq, ok := icmpReq.Body.(*icmp.Echo)
	if !ok {
		return 0, fmt.Errorf("error parsing ICMP echo request: unexpected object type: %T", icmpReq.Body)
	}

	replyBuf, err := createEchoReply(echoReq, mpc.returnSpurious)
	if err != nil {
		return 0, err
	}

	// Add reply to replies channel. This will be picked up by the Pinger's
	// reply-processing goroutine
	reply := reply{replyBuf, peer}
	mpc.replyChan <- reply
	return len(buf), nil
}

func (mpc *mockPingConn) ReadFrom(buf []byte) (read int, peer net.Addr, err error) {
	if mpc.returnErrOnRead {
		return 0, nil, errRead
	}

	// Read reply from replies channel, and copy the data into the buffer
	// provided by the Pinger
	select {
	case <-mpc.closed:
		return 0, nil, errClosedConn
	case reply := <-mpc.replyChan:
		copy(buf, reply.data)
		return len(reply.data), reply.peer, nil
	}
}

// Mark as closed, so any subsequent reads by the Pinger return an error
func (mpc *mockPingConn) Close() error {
	// Cause further reads to return an error
	close(mpc.closed)
	return nil
}

func createEchoReply(req *icmp.Echo, spurious bool) ([]byte, error) {
	reply := icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: icmpCodeEcho,
		Body: &icmp.Echo{
			ID:   req.ID,
			Data: req.Data,
			Seq:  req.Seq,
		},
	}

	if spurious {
		reply.Body.(*icmp.Echo).Seq = 0xBAD
	}

	buf, err := reply.Marshal(nil)
	if err != nil {
		return nil, fmt.Errorf("marshalling ICMP Echo reply message: %w", err)
	}

	return buf, nil
}
