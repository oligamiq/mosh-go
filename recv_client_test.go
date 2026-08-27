package mosh

import (
	"io"
	"sync"
	"testing"
	"time"

	vt "github.com/unixshells/vt-go"
)

type queuedDatagramConn struct {
	readC  chan []byte
	closed chan struct{}
	once   sync.Once
}

func newQueuedDatagramConn() *queuedDatagramConn {
	return &queuedDatagramConn{readC: make(chan []byte, 16), closed: make(chan struct{})}
}

func (c *queuedDatagramConn) Read(b []byte) (int, error) {
	select {
	case data := <-c.readC:
		return copy(b, data), nil
	case <-c.closed:
		return 0, io.EOF
	}
}

func (c *queuedDatagramConn) Write(b []byte) (int, error)     { return len(b), nil }
func (c *queuedDatagramConn) SetReadDeadline(time.Time) error { return nil }
func (c *queuedDatagramConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func TestClientRecvLoopRecoversBranchedServerState(t *testing.T) {
	key := make([]byte, 16)
	serverOCB, err := NewOCB(key)
	if err != nil {
		t.Fatal(err)
	}
	clientOCB, err := NewOCB(key)
	if err != nil {
		t.Fatal(err)
	}

	server := NewTransport(serverOCB, true)
	conn := newQueuedDatagramConn()
	client, err := DialConnTerminal(conn, clientOCB)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	presentation := client.Recv(time.Second)
	if len(presentation) == 0 {
		t.Fatal("no terminal presentation output")
	}

	send := func(hoststring string) {
		server.SetPending(MarshalHostMessage([]HostInstruction{{Hoststring: []byte(hoststring), EchoAckNum: -1}}))
		for _, dg := range server.Tick() {
			conn.readC <- dg
		}
	}

	send("\x1b[HAAAA")
	first := client.Recv(time.Second)
	if len(first) == 0 {
		t.Fatal("no first output")
	}

	// The server has not received a client ack, so this second state is also
	// rooted at state 0. The client must recover from displayed state 1 to it.
	send("\x1b[HBBBB")
	second := client.Recv(time.Second)
	if len(second) == 0 {
		t.Fatal("no recovery output")
	}

	emu := vt.NewEmulator(80, 24)
	_, _ = emu.Write(presentation)
	_, _ = emu.Write(first)
	_, _ = emu.Write(second)
	for x, want := range "BBBB" {
		cell := emu.CellAt(x, 0)
		if cell == nil || cell.Content != string(want) {
			t.Fatalf("cell %d = %#v, want %q", x, cell, string(want))
		}
	}
}

func TestClientRecvTreatsOutputSignalAsWakeupHint(t *testing.T) {
	client := &Client{outputC: make(chan struct{}, 1), done: make(chan struct{})}
	client.mu.Lock()
	client.output = []byte("first")
	client.mu.Unlock()
	client.outputC <- struct{}{} // deliberately leave a stale signal behind

	if got := string(client.Recv(time.Second)); got != "first" {
		t.Fatalf("first Recv = %q", got)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		client.mu.Lock()
		client.output = []byte("second")
		client.mu.Unlock()
		select {
		case client.outputC <- struct{}{}:
		default:
		}
	}()
	if got := string(client.Recv(time.Second)); got != "second" {
		t.Fatalf("second Recv = %q; stale wakeup returned early", got)
	}
}

func TestRawDialConnDoesNotEnableTerminalStateRecovery(t *testing.T) {
	key := make([]byte, 16)
	serverOCB, err := NewOCB(key)
	if err != nil {
		t.Fatal(err)
	}
	clientOCB, err := NewOCB(key)
	if err != nil {
		t.Fatal(err)
	}
	server := NewTransport(serverOCB, true)
	conn := newQueuedDatagramConn()
	client, err := DialConn(conn, clientOCB)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.recvState != nil {
		t.Fatal("raw DialConn unexpectedly enabled terminal state recovery")
	}

	server.SetPending(MarshalHostMessage([]HostInstruction{{Hoststring: []byte("raw-hoststring"), EchoAckNum: -1}}))
	for _, dg := range server.Tick() {
		conn.readC <- dg
	}
	if got := string(client.Recv(time.Second)); got != "raw-hoststring" {
		t.Fatalf("raw DialConn output = %q", got)
	}
}
