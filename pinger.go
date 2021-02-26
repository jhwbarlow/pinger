// Package pinger implements a Pinger type, used
// to ping multiple IPv4 peers at an interval,
// with the ability to register callback functions
// to be called upon a successful ping or a timeout.
package pinger

import (
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const (
	bind             = "0.0.0.0"
	icmpCodeEcho     = 0
	icmpProtoNoIPv4  = 1
	ipv4MaxPacketLen = 65535
)

type pingState struct {
	ip  *net.IPAddr
	seq int
}

func (ps *pingState) nextSeq() int {
	if ps.seq == math.MaxInt16 { // ICMP sequence is 16 bits wide
		ps.seq = 0
	}

	ps.seq++
	return ps.seq
}

type pingConn interface {
	WriteTo(buf []byte, peer net.Addr) (written int, err error)
	ReadFrom(buf []byte) (read int, peer net.Addr, err error)
	Close() error
}

type pingConnCreateFunc func() (pingConn, error)

// createPingConn is a wrapper for icmp.ListenPacket(), converting the concrete
// return type of that function to an interface type, to allow mocking of the conn.
func createPingConn() (pingConn, error) {
	return icmp.ListenPacket("ip4:1", bind) // '1' is the IPv4 protocol no. for ICMP
}

// HandlerFunc is a callback function called when a ping reply is
// received or a timeout occurs.
type HandlerFunc func(peerIP net.IP, seq int)

// SpuriousHandlerFunc is a callback function called when a spurious or
// late ICMP message is received.
type SpuriousHandlerFunc func(peerIP net.IP)

// Pinger repeatedly pings peers at a time given by Interval with a
// timeout given by Timeout. The data to send in each ICMP packet is
// specified in Data. Optional callback functions can be
// provided in SuccessHandler and TimeoutHandler. These functions
// are executed synchronously, so users of Pinger should start their
// own goroutines in these callback functions if required.
type Pinger struct {
	Interval, Timeout              time.Duration
	SuccessHandler, TimeoutHandler HandlerFunc
	SpuriousHandler                SpuriousHandlerFunc
	Data                           []byte

	pingConnCreateFunc pingConnCreateFunc
	conn               pingConn
	timeoutTable       *timeoutTable

	peersMutex sync.Mutex
	peers      map[string]*pingState
	id         int
}

// New constructs a new Pinger. It does not initialise resources
// such as network connections. For that, Init must be called
// subsequently.
func New(interval, timeout time.Duration, data []byte) (*Pinger, error) {
	if interval <= 0 {
		return nil, errors.New("interval must be greater than zero")
	}

	if timeout <= 0 {
		return nil, errors.New("timeout must be greater than zero")
	}

	return &Pinger{
		Interval:           interval,
		Timeout:            timeout,
		Data:               data,
		pingConnCreateFunc: createPingConn,
	}, nil
}

// WithTimeoutHandler installs the provided HandlerFunc as the handler
// for timed-out ping requests.
func (p *Pinger) WithTimeoutHandler(timeoutHandler HandlerFunc) *Pinger {
	p.TimeoutHandler = timeoutHandler
	return p
}

// WithSuccessHandler installs the provided HandlerFunc as the handler
// for sucessfully responded ping requests.
func (p *Pinger) WithSuccessHandler(successHandler HandlerFunc) *Pinger {
	p.SuccessHandler = successHandler
	return p
}

// WithSpuriousHandler installs the provided SpuriousHandlerFunc as the handler
// for spuriously received ICMP messages and late ping responses.
func (p *Pinger) WithSpuriousHandler(spuriousHandler SpuriousHandlerFunc) *Pinger {
	p.SpuriousHandler = spuriousHandler
	return p
}

// Init initialises a newly constructed Pinger, initialising resources
// such as network connections. It does not start pinging. For that,
// Run must be called subsequently.
func (p *Pinger) Init() error {
	var err error

	p.peers = make(map[string]*pingState, 256)

	// Ensure each instance gets a unique 16 bit ICMP ID (cf. UDP/TCP port)
	rand.Seed(int64(os.Getpid()))
	p.id = rand.Intn(math.MaxInt16)

	p.timeoutTable, err = newTimeoutTable(p.Timeout, p.handleTimeout)
	if err != nil {
		return fmt.Errorf("constructing timeout map: %w", err)
	}

	p.conn, err = p.pingConnCreateFunc()
	if err != nil {
		return fmt.Errorf("ICMP listening: %w", err)
	}

	return nil
}

// AddPeer adds a peer to the Pinger. When the next interval time arrives,
// the peer will be pinged. Peer in IPv4 dotted-decimal notation.
func (p *Pinger) AddPeer(peer string) error {
	ip := net.ParseIP(peer)
	if ip == nil {
		return fmt.Errorf("invalid IP address: %q", peer)
	}

	ipAddr := &net.IPAddr{IP: ip}

	p.peersMutex.Lock()
	p.peers[peer] = &pingState{ip: ipAddr}
	p.peersMutex.Unlock()

	return nil
}

// RemovePeer removes a peer from the Pinger, such that it will no longer be pinged.
// Peer in IPv4 dotted-decimal notation.
func (p *Pinger) RemovePeer(peer string) {
	p.peersMutex.Lock()
	delete(p.peers, peer)
	p.peersMutex.Unlock()
}

// Run starts the Pinger. Any peers added will start to be pinged after the first interval
// time and every interval after that, until the peer is removed, or the done channel is closed.
// Once done is closed, the Pinger instance cannot be reused.
func (p *Pinger) Run(done <-chan struct{}) <-chan error {
	respsErrChan := p.receiveResps(done)
	runErrChan := p.run(done)

	// Multiplex errors from the receiveResps() and run() goroutines
	// onto a single channel. This goroutine "owns" the resultant errChan,
	// and will only close it when the Pinger is shutdown by a close of
	// the done channel
	errChan := make(chan error)
	go func(respsErrChan, runErrChan <-chan error, errChan chan<- error) {
	loop:
		for {
			select {
			case err := <-respsErrChan:
				errChan <- err
			case err := <-runErrChan:
				errChan <- err
			case <-done:
				break loop
			}
		}

		close(errChan)
	}(respsErrChan, runErrChan, errChan)

	return errChan
}

func (p *Pinger) run(done <-chan struct{}) <-chan error {
	errChan := make(chan error)

	go func(errChan chan<- error) {
		ticker := time.Tick(p.Interval)
	loop:
		for {
			select {
			case <-done:
				p.destroy()
				break loop
			case <-ticker:
				if err := p.sweep(); err != nil {
					errChan <- fmt.Errorf("sweeping network: %w", err)
				}
			}
		}

		close(errChan)
	}(errChan)

	return errChan
}

func (p *Pinger) destroy() {
	p.conn.Close()
	p.timeoutTable.cleardown()
}

func (p *Pinger) sweep() error {
	if len(p.peers) == 0 {
		return nil
	}

	p.peersMutex.Lock()
	peers := make([]*pingState, 0, len(p.peers))
	for _, peer := range p.peers {
		peers = append(peers, peer)
	}
	p.peersMutex.Unlock()

	for _, peer := range peers {
		if err := p.sendReq(peer); err != nil {
			return fmt.Errorf("pinging %q: %w", peer.ip.IP, err)
		}
	}

	return nil
}

func (p *Pinger) sendReq(peer *pingState) error {
	echoReq := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: icmpCodeEcho,
		Body: &icmp.Echo{
			ID:   p.id,
			Data: p.Data,
		},
	}

	// Create ICMP echo message
	seq := peer.nextSeq()
	echoReq.Body.(*icmp.Echo).Seq = seq
	buf, err := echoReq.Marshal(nil)
	if err != nil {
		return fmt.Errorf("marshalling ICMP Echo message: %w", err)
	}

	// Add to timeout map
	if err := p.timeoutTable.add(peer.ip.IP, seq); err != nil {
		return fmt.Errorf("storing in timeout map: %w", err)
	}

	// Write to wire
	written, err := p.conn.WriteTo(buf, peer.ip)
	if err != nil {
		return fmt.Errorf("writing ICMP message to network: %w", err)
	}
	if written != len(buf) {
		return errors.New("writing ICMP message to network: short write")
	}

	return nil
}

func (p *Pinger) receiveResps(done <-chan struct{}) <-chan error {
	errChan := make(chan error)
	go func(errChan chan<- error) {
		buf := make([]byte, ipv4MaxPacketLen)

	loop:
		for {
			select {
			case <-done:
				break loop
			default:
			}

			read, peerAddr, err := p.conn.ReadFrom(buf)
			if err != nil {
				select {
				case <-done:
					// If the Pinger has been cancelled, then the conn will be closed
					// and the ReadFrom() will return from blocking with an error
					break loop
				default:
					log.Printf("error reading ICMP message from network: %v", err)
					errChan <- fmt.Errorf("error reading ICMP message from network: %w", err)
					continue
				}
			}
			log.Printf("read message of %d bytes from %v", read, peerAddr)

			icmpResp, err := icmp.ParseMessage(icmpProtoNoIPv4, buf[:read])
			if err != nil {
				// Discard as spurious
				log.Printf("error parsing ICMP message from network: %v", err)
				p.handleSpurious(peerAddr.(*net.IPAddr).IP)
				continue
			}

			if icmpResp.Type != ipv4.ICMPTypeEchoReply {
				// Discard as spurious
				log.Printf("error parsing ICMP echo response: unexpected ICMP type: %d", icmpResp.Type)
				p.handleSpurious(peerAddr.(*net.IPAddr).IP)
				continue
			}

			echoResp, ok := icmpResp.Body.(*icmp.Echo)
			if !ok {
				// Discard as spurious
				log.Printf("error parsing ICMP echo response: unexpected type: %T", icmpResp.Body)
				p.handleSpurious(peerAddr.(*net.IPAddr).IP)
				continue
			}

			if !p.timeoutTable.remove(peerAddr.(*net.IPAddr).IP, echoResp.Seq) {
				log.Printf("received unexpected or late echo reply from %v with sequence %d: %q", peerAddr, echoResp.Seq, string(echoResp.Data))
				p.handleSpurious(peerAddr.(*net.IPAddr).IP)
				continue // Will have already timed out or is spurious and doesnt match
			}

			log.Printf("received echo reply from %v with sequence %d: %q", peerAddr, echoResp.Seq, string(echoResp.Data))

			if p.SuccessHandler != nil {
				p.SuccessHandler(peerAddr.(*net.IPAddr).IP, echoResp.Seq)
			}
		}

		close(errChan)
	}(errChan)

	return errChan
}

func (p *Pinger) handleTimeout(ip net.IP, seq int) {
	log.Printf("timed-out waiting for reply from %v with sequence %d", ip, seq)

	if p.TimeoutHandler != nil {
		p.TimeoutHandler(ip, seq)
	}
}

func (p *Pinger) handleSpurious(ip net.IP) {
	if p.SpuriousHandler != nil {
		p.SpuriousHandler(ip)
	}
}
