package cast

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/telnesstech/whitenoise-caster/cast/proto/v1"
)

// CASTV2 namespaces.
const (
	nsConnection = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver   = "urn:x-cast:com.google.cast.receiver"
	nsMedia      = "urn:x-cast:com.google.cast.media"
)

// Default media receiver app ID.
const defaultMediaReceiverAppID = "CC1AD845"

// Platform receiver virtual ID.
const platformReceiverID = "receiver-0"

// Client manages a CASTV2 session with a Chromecast device.
type Client struct {
	log  *slog.Logger
	conn *Conn

	mu      sync.Mutex
	pending map[int]chan json.RawMessage

	nextReqID      atomic.Int64
	transportID    string
	mediaSessionID int

	cancel context.CancelFunc
	done   chan struct{}
}

// mediaStatus mirrors the relevant fields from MEDIA_STATUS responses.
type mediaStatus struct {
	PlayerState string `json:"playerState"`
}

// Connect dials the Chromecast and starts the heartbeat and read loops.
func Connect(ctx context.Context, addr string, port int, timeout time.Duration, log *slog.Logger) (*Client, error) {
	conn, err := Dial(addr, port, timeout)
	if err != nil {
		return nil, err
	}

	loopCtx, cancel := context.WithCancel(context.Background())

	c := &Client{
		log:     log,
		conn:    conn,
		pending: make(map[int]chan json.RawMessage),
		cancel:  cancel,
		done:    make(chan struct{}),
	}

	go c.readLoop(loopCtx)
	go c.heartbeatLoop(loopCtx)

	// Virtual connection to the platform receiver.
	if err := c.sendConnect(platformReceiverID); err != nil {
		cancel()
		conn.Close()
		return nil, fmt.Errorf("connect to receiver: %w", err)
	}

	return c, nil
}

// LaunchMediaReceiver launches the default media receiver and connects to it.
func (c *Client) LaunchMediaReceiver(ctx context.Context) error {
	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"LAUNCH","appId":"%s","requestId":%d}`, defaultMediaReceiverAppID, reqID)

	if err := c.sendJSON(platformReceiverID, nsReceiver, payload); err != nil {
		return fmt.Errorf("send LAUNCH: %w", err)
	}

	resp, err := c.waitResponse(ctx, reqID)
	if err != nil {
		return fmt.Errorf("LAUNCH response: %w", err)
	}

	transportID, err := extractTransportID(resp)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.transportID = transportID
	c.mu.Unlock()

	// Virtual connection to the media receiver transport.
	if err := c.sendConnect(transportID); err != nil {
		return fmt.Errorf("connect to transport: %w", err)
	}

	return nil
}

// LoadMedia loads a media URL for playback with autoplay.
func (c *Client) LoadMedia(ctx context.Context, url, contentType string) error {
	c.mu.Lock()
	transportID := c.transportID
	c.mu.Unlock()

	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"LOAD","requestId":%d,"autoplay":true,"media":{"contentId":"%s","contentType":"%s","streamType":"BUFFERED"}}`,
		reqID, url, contentType)

	if err := c.sendJSON(transportID, nsMedia, payload); err != nil {
		return fmt.Errorf("send LOAD: %w", err)
	}

	resp, err := c.waitResponse(ctx, reqID)
	if err != nil {
		return fmt.Errorf("LOAD response: %w", err)
	}

	msID, err := extractMediaSessionID(resp)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.mediaSessionID = msID
	c.mu.Unlock()

	return nil
}

// Play sends a PLAY command.
func (c *Client) Play(ctx context.Context) error {
	return c.sendMediaCommand(ctx, "PLAY")
}

// Pause sends a PAUSE command.
func (c *Client) Pause(ctx context.Context) error {
	return c.sendMediaCommand(ctx, "PAUSE")
}

// StopMedia sends a STOP command on the media namespace.
func (c *Client) StopMedia(ctx context.Context) error {
	return c.sendMediaCommand(ctx, "STOP")
}

// SetVolume sets the volume level (0.0–1.0) on the receiver.
func (c *Client) SetVolume(ctx context.Context, level float32) error {
	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"SET_VOLUME","volume":{"level":%f},"requestId":%d}`, level, reqID)

	if err := c.sendJSON(platformReceiverID, nsReceiver, payload); err != nil {
		return fmt.Errorf("send SET_VOLUME: %w", err)
	}

	if _, err := c.waitResponse(ctx, reqID); err != nil {
		return fmt.Errorf("SET_VOLUME response: %w", err)
	}
	return nil
}

// GetMediaStatus polls for the current media status.
func (c *Client) GetMediaStatus(ctx context.Context) (*mediaStatus, error) {
	c.mu.Lock()
	transportID := c.transportID
	c.mu.Unlock()

	if transportID == "" {
		return nil, fmt.Errorf("no active transport")
	}

	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"GET_STATUS","requestId":%d}`, reqID)

	if err := c.sendJSON(transportID, nsMedia, payload); err != nil {
		return nil, fmt.Errorf("send GET_STATUS: %w", err)
	}

	resp, err := c.waitResponse(ctx, reqID)
	if err != nil {
		return nil, fmt.Errorf("GET_STATUS response: %w", err)
	}

	var envelope struct {
		Status []struct {
			MediaSessionID int    `json:"mediaSessionId"`
			PlayerState    string `json:"playerState"`
		} `json:"status"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		return nil, fmt.Errorf("parse MEDIA_STATUS: %w", err)
	}

	if len(envelope.Status) == 0 {
		return &mediaStatus{PlayerState: "IDLE"}, nil
	}

	// Update our stored mediaSessionID from the latest status.
	s := envelope.Status[0]
	c.mu.Lock()
	c.mediaSessionID = s.MediaSessionID
	c.mu.Unlock()

	return &mediaStatus{PlayerState: s.PlayerState}, nil
}

// Close tears down the session: sends CLOSE messages, cancels goroutines,
// and closes the TCP connection.
func (c *Client) Close() {
	c.mu.Lock()
	transportID := c.transportID
	c.mu.Unlock()

	// Best-effort CLOSE to transport then receiver.
	if transportID != "" {
		_ = c.sendJSON(transportID, nsConnection, `{"type":"CLOSE"}`)
	}
	_ = c.sendJSON(platformReceiverID, nsConnection, `{"type":"CLOSE"}`)

	c.cancel()
	<-c.done // wait for readLoop to exit
	_ = c.conn.Close()
}

// --- internal ---

func (c *Client) allocReqID() int {
	return int(c.nextReqID.Add(1))
}

func (c *Client) sendConnect(destID string) error {
	return c.sendJSON(destID, nsConnection, `{"type":"CONNECT"}`)
}

func (c *Client) sendJSON(destID, namespace, payload string) error {
	msg := &pb.CastMessage{
		ProtocolVersion: pb.CastMessage_PROTOCOL_VERSION_CASTV2_1_0,
		SourceId:        "sender-0",
		DestinationId:   destID,
		Namespace:       namespace,
		PayloadType:     pb.CastMessage_PAYLOAD_TYPE_STRING,
		PayloadUtf8:     payload,
	}
	return c.conn.Send(msg)
}

func (c *Client) sendMediaCommand(ctx context.Context, cmdType string) error {
	c.mu.Lock()
	transportID := c.transportID
	msID := c.mediaSessionID
	c.mu.Unlock()

	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"%s","mediaSessionId":%d,"requestId":%d}`, cmdType, msID, reqID)

	if err := c.sendJSON(transportID, nsMedia, payload); err != nil {
		return fmt.Errorf("send %s: %w", cmdType, err)
	}

	if _, err := c.waitResponse(ctx, reqID); err != nil {
		return fmt.Errorf("%s response: %w", cmdType, err)
	}
	return nil
}

func (c *Client) waitResponse(ctx context.Context, reqID int) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)

	c.mu.Lock()
	c.pending[reqID] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
	}()

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.done)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := c.conn.Recv()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				c.log.Debug("readLoop recv error", "error", err)
				return
			}
		}

		ns := msg.GetNamespace()
		payload := msg.GetPayloadUtf8()

		switch ns {
		case nsHeartbeat:
			// Respond to PINGs from the device.
			var hb struct{ Type string }
			if json.Unmarshal([]byte(payload), &hb) == nil && hb.Type == "PING" {
				_ = c.sendJSON(platformReceiverID, nsHeartbeat, `{"type":"PONG"}`)
			}

		case nsConnection:
			// CLOSE from device — log and continue; the controller will
			// detect the failure via status polling.
			c.log.Debug("connection message", "payload", payload)

		default:
			// Dispatch by requestId to any waiting caller.
			var envelope struct {
				RequestID int `json:"requestId"`
			}
			if json.Unmarshal([]byte(payload), &envelope) == nil && envelope.RequestID > 0 {
				c.mu.Lock()
				ch, ok := c.pending[envelope.RequestID]
				c.mu.Unlock()
				if ok {
					ch <- json.RawMessage(payload)
				}
			}
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.sendJSON(platformReceiverID, nsHeartbeat, `{"type":"PING"}`); err != nil {
				c.log.Debug("heartbeat send error", "error", err)
				return
			}
		}
	}
}

// extractTransportID pulls the transportId from a RECEIVER_STATUS response.
func extractTransportID(data json.RawMessage) (string, error) {
	var resp struct {
		Status struct {
			Applications []struct {
				TransportID string `json:"transportId"`
			} `json:"applications"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse RECEIVER_STATUS: %w", err)
	}
	if len(resp.Status.Applications) == 0 {
		return "", fmt.Errorf("no applications in RECEIVER_STATUS")
	}
	tid := resp.Status.Applications[0].TransportID
	if tid == "" {
		return "", fmt.Errorf("empty transportId in RECEIVER_STATUS")
	}
	return tid, nil
}

// extractMediaSessionID pulls the mediaSessionId from a MEDIA_STATUS response.
func extractMediaSessionID(data json.RawMessage) (int, error) {
	var resp struct {
		Status []struct {
			MediaSessionID int `json:"mediaSessionId"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, fmt.Errorf("parse MEDIA_STATUS: %w", err)
	}
	if len(resp.Status) == 0 {
		return 0, fmt.Errorf("no status entries in MEDIA_STATUS")
	}
	return resp.Status[0].MediaSessionID, nil
}
