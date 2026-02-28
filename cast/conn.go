package cast

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	pb "github.com/telnesstech/whitenoise-caster/cast/proto/v1"
	"google.golang.org/protobuf/proto"
)

// Conn is a low-level TLS connection to a Chromecast device.
// It handles 4-byte big-endian length-prefixed protobuf message framing.
type Conn struct {
	conn *tls.Conn
}

// Dial establishes a TLS connection to a Chromecast at addr:port.
// Chromecast devices use self-signed certificates, so verification is skipped.
func Dial(addr string, port int, timeout time.Duration) (*Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", fmt.Sprintf("%s:%d", addr, port), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, fmt.Errorf("tls dial: %w", err)
	}
	return &Conn{conn: conn}, nil
}

// Send marshals a CastMessage and writes it as [4-byte BE length][payload].
func (c *Conn) Send(msg *pb.CastMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf, uint32(len(data)))
	copy(buf[4:], data)

	_, err = c.conn.Write(buf)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// Recv reads one length-prefixed protobuf message. Blocks until a message
// arrives or the connection is closed.
func (c *Conn) Recv() (*pb.CastMessage, error) {
	var length uint32
	if err := binary.Read(c.conn, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}

	if length > 1<<20 { // 1 MB sanity limit
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(c.conn, buf); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	msg := &pb.CastMessage{}
	if err := proto.Unmarshal(buf, msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return msg, nil
}

// Close closes the underlying TLS connection.
func (c *Conn) Close() error {
	return c.conn.Close()
}
