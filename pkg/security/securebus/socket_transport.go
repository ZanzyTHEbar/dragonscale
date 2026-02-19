package securebus

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/itr"
)

const (
	maxFrameSize    = 16 * 1024 * 1024 // 16 MiB sanity limit
	socketFilePerms = 0600
)

// SocketTransport implements Transport over a Unix domain socket using
// length-prefixed JSON frames (4-byte big-endian length + JSON payload).
// The server side listens for connections and dispatches requests to the
// SecureBus; the client side connects and performs request/response exchanges.
type SocketTransport struct {
	mu       sync.Mutex
	path     string
	conn     net.Conn
	listener net.Listener
	closed   chan struct{}
	isServer bool

	connsMu sync.Mutex
	conns   []net.Conn
}

// NewSocketTransportClient connects to the daemon's Unix socket at path.
func NewSocketTransportClient(path string) (*SocketTransport, error) {
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s: %w", path, err)
	}
	return &SocketTransport{
		path:   path,
		conn:   conn,
		closed: make(chan struct{}),
	}, nil
}

// NewSocketTransportServer creates a listening Unix socket at path.
// Call Serve() to start accepting connections.
func NewSocketTransportServer(path string) (*SocketTransport, error) {
	_ = os.Remove(path)

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}

	if err := os.Chmod(path, socketFilePerms); err != nil {
		listener.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	return &SocketTransport{
		path:     path,
		listener: listener,
		closed:   make(chan struct{}),
		isServer: true,
	}, nil
}

// Send submits a request over the socket and blocks until the response arrives.
// Client-side only.
func (st *SocketTransport) Send(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error) {
	if st.isServer {
		return itr.ToolResponse{}, fmt.Errorf("Send called on server transport; use Serve instead")
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	select {
	case <-st.closed:
		return itr.ToolResponse{}, fmt.Errorf("transport closed")
	default:
	}

	if err := writeFrame(st.conn, req); err != nil {
		return itr.ToolResponse{}, fmt.Errorf("write request: %w", err)
	}

	var resp itr.ToolResponse
	if err := readFrame(st.conn, &resp); err != nil {
		return itr.ToolResponse{}, fmt.Errorf("read response: %w", err)
	}

	return resp, nil
}

// Serve accepts connections and dispatches requests to handler. Blocks until
// Close is called or the listener errors. Server-side only.
func (st *SocketTransport) Serve(handler func(ctx context.Context, req itr.ToolRequest) itr.ToolResponse) error {
	if !st.isServer {
		return fmt.Errorf("Serve called on client transport")
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := st.listener.Accept()
		if err != nil {
			select {
			case <-st.closed:
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}

		st.connsMu.Lock()
		st.conns = append(st.conns, conn)
		st.connsMu.Unlock()

		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer c.Close()
			st.handleConnection(c, handler)
		}(conn)
	}
}

func (st *SocketTransport) handleConnection(conn net.Conn, handler func(ctx context.Context, req itr.ToolRequest) itr.ToolResponse) {
	for {
		select {
		case <-st.closed:
			return
		default:
		}

		var req itr.ToolRequest
		if err := readFrame(conn, &req); err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		resp := handler(context.Background(), req)

		if err := writeFrame(conn, resp); err != nil {
			return
		}
	}
}

// Path returns the socket file path.
func (st *SocketTransport) Path() string {
	return st.path
}

// Close shuts down the transport.
func (st *SocketTransport) Close() error {
	select {
	case <-st.closed:
		return nil
	default:
		close(st.closed)
	}

	if st.listener != nil {
		st.listener.Close()
		_ = os.Remove(st.path)
	}

	st.connsMu.Lock()
	for _, c := range st.conns {
		c.Close()
	}
	st.conns = nil
	st.connsMu.Unlock()

	if st.conn != nil {
		st.conn.Close()
	}
	return nil
}

// writeFrame writes a length-prefixed JSON frame to w.
func writeFrame(w io.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if len(data) > maxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(data), maxFrameSize)
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))

	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// readFrame reads a length-prefixed JSON frame from r into v.
func readFrame(r io.Reader, v interface{}) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}

	size := binary.BigEndian.Uint32(header[:])
	if size > maxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", size, maxFrameSize)
	}

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}

	return json.Unmarshal(buf, v)
}
