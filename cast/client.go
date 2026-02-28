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
	"google.golang.org/protobuf/proto"
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
	log.Info("dialing chromecast", "addr", addr, "port", port, "timeout", timeout)
	conn, err := Dial(addr, port, timeout)
	if err != nil {
		log.Error("dial failed", "addr", addr, "error", err)
		return nil, err
	}
	log.Info("dial succeeded", "addr", addr)

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
	log.Info("sending CONNECT to platform receiver")
	if err := c.sendConnect(platformReceiverID); err != nil {
		log.Error("connect to receiver failed", "error", err)
		cancel()
		conn.Close()
		return nil, fmt.Errorf("connect to receiver: %w", err)
	}
	log.Info("connected to platform receiver")

	return c, nil
}

// LaunchMediaReceiver launches the default media receiver and connects to it.
func (c *Client) LaunchMediaReceiver(ctx context.Context) error {
	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"LAUNCH","appId":"%s","requestId":%d}`, defaultMediaReceiverAppID, reqID)

	c.log.Info("sending LAUNCH", "appId", defaultMediaReceiverAppID, "reqID", reqID)
	if err := c.sendJSON(platformReceiverID, nsReceiver, payload); err != nil {
		return fmt.Errorf("send LAUNCH: %w", err)
	}

	c.log.Info("waiting for LAUNCH response", "reqID", reqID)
	resp, err := c.waitResponse(ctx, reqID)
	if err != nil {
		c.log.Error("LAUNCH response failed", "reqID", reqID, "error", err)
		return fmt.Errorf("LAUNCH response: %w", err)
	}
	c.log.Info("LAUNCH response received", "reqID", reqID)

	transportID, err := extractTransportID(resp)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.transportID = transportID
	c.mu.Unlock()

	// Virtual connection to the media receiver transport.
	c.log.Info("sending CONNECT to transport", "transportID", transportID)
	if err := c.sendConnect(transportID); err != nil {
		return fmt.Errorf("connect to transport: %w", err)
	}
	c.log.Info("connected to transport", "transportID", transportID)

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

	c.log.Info("sending LOAD", "reqID", reqID, "url", url, "contentType", contentType, "transportID", transportID)
	if err := c.sendJSON(transportID, nsMedia, payload); err != nil {
		return fmt.Errorf("send LOAD: %w", err)
	}

	c.log.Info("waiting for LOAD response", "reqID", reqID)
	resp, err := c.waitResponse(ctx, reqID)
	if err != nil {
		c.log.Error("LOAD response failed", "reqID", reqID, "error", err)
		return fmt.Errorf("LOAD response: %w", err)
	}
	c.log.Info("LOAD response received", "reqID", reqID)

	msID, err := extractMediaSessionID(resp)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.mediaSessionID = msID
	c.mu.Unlock()

	c.log.Info("media loaded", "mediaSessionID", msID)
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

// SetMuted mutes or unmutes the receiver output without changing the volume level.
func (c *Client) SetMuted(ctx context.Context, muted bool) error {
	reqID := c.allocReqID()
	payload := fmt.Sprintf(`{"type":"SET_VOLUME","volume":{"muted":%t},"requestId":%d}`, muted, reqID)

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

	c.log.Debug("sending GET_STATUS", "reqID", reqID, "transportID", transportID)
	if err := c.sendJSON(transportID, nsMedia, payload); err != nil {
		return nil, fmt.Errorf("send GET_STATUS: %w", err)
	}

	c.log.Debug("waiting for GET_STATUS response", "reqID", reqID)
	resp, err := c.waitResponse(ctx, reqID)
	if err != nil {
		c.log.Error("GET_STATUS response failed", "reqID", reqID, "error", err)
		return nil, fmt.Errorf("GET_STATUS response: %w", err)
	}
	c.log.Debug("GET_STATUS response received", "reqID", reqID)

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

	c.log.Info("client closing", "transportID", transportID)

	// Best-effort CLOSE to transport then receiver.
	if transportID != "" {
		c.log.Debug("sending CLOSE to transport", "transportID", transportID)
		_ = c.sendJSON(transportID, nsConnection, `{"type":"CLOSE"}`)
	}
	c.log.Debug("sending CLOSE to platform receiver")
	_ = c.sendJSON(platformReceiverID, nsConnection, `{"type":"CLOSE"}`)

	// Close the TCP connection first so the blocking Recv() in readLoop
	// returns an error, then cancel the context and wait for readLoop to exit.
	c.log.Debug("closing TCP connection to unblock readLoop")
	_ = c.conn.Close()
	c.log.Debug("cancelling context, waiting for readLoop to exit")
	c.cancel()
	<-c.done
	c.log.Info("client closed")
}

// --- internal ---

func (c *Client) allocReqID() int {
	return int(c.nextReqID.Add(1))
}

func (c *Client) sendConnect(destID string) error {
	return c.sendJSON(destID, nsConnection, `{"type":"CONNECT"}`)
}

func (c *Client) sendJSON(destID, namespace, payloadStr string) error {
	msg := &pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        proto.String("sender-0"),
		DestinationId:   proto.String(destID),
		Namespace:       proto.String(namespace),
		PayloadType:     pb.CastMessage_STRING.Enum(),
		PayloadUtf8:     proto.String(payloadStr),
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

	c.log.Info("sending media command", "type", cmdType, "reqID", reqID, "mediaSessionID", msID, "transportID", transportID)
	if err := c.sendJSON(transportID, nsMedia, payload); err != nil {
		return fmt.Errorf("send %s: %w", cmdType, err)
	}

	c.log.Info("waiting for media command response", "type", cmdType, "reqID", reqID)
	if _, err := c.waitResponse(ctx, reqID); err != nil {
		c.log.Error("media command response failed", "type", cmdType, "reqID", reqID, "error", err)
		return fmt.Errorf("%s response: %w", cmdType, err)
	}
	c.log.Info("media command succeeded", "type", cmdType, "reqID", reqID)
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
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
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
		src := msg.GetSourceId()
		dst := msg.GetDestinationId()

		c.log.Debug("recv", "ns", ns, "src", src, "dst", dst, "payload", payload)

		switch ns {
		case nsHeartbeat:
			// Respond to PINGs from the device.
			var hb struct{ Type string }
			if json.Unmarshal([]byte(payload), &hb) == nil && hb.Type == "PING" {
				_ = c.sendJSON(platformReceiverID, nsHeartbeat, `{"type":"PONG"}`)
			}

		case nsConnection:
			var cm struct{ Type string }
			if json.Unmarshal([]byte(payload), &cm) == nil && cm.Type == "CLOSE" {
				c.mu.Lock()
				isTransportClose := c.transportID != "" && src == c.transportID
				c.mu.Unlock()

				if isTransportClose {
					// The media receiver app was killed by the device (idle timeout).
					// The session is dead — close the connection so the controller
					// can detect it and recover on the next interaction.
					c.log.Info("transport closed by device, session is dead", "src", src)
					_ = c.conn.Close()
					return
				}

				// Platform-level CLOSE (e.g., device resets during LAUNCH);
				// re-send CONNECT so it keeps talking to us.
				c.log.Info("received CLOSE from device, re-sending CONNECT", "src", src)
				if err := c.sendConnect(platformReceiverID); err != nil {
					c.log.Error("re-connect after CLOSE failed", "error", err)
					return
				}
				continue
			}
			c.log.Info("connection message from device", "src", src, "payload", payload)

		default:
			// Dispatch by requestId to any waiting caller.
			var envelope struct {
				RequestID int    `json:"requestId"`
				Type      string `json:"type"`
			}
			if json.Unmarshal([]byte(payload), &envelope) == nil && envelope.RequestID > 0 {
				c.log.Debug("dispatching response", "reqID", envelope.RequestID, "type", envelope.Type)
				c.mu.Lock()
				ch, ok := c.pending[envelope.RequestID]
				c.mu.Unlock()
				if ok {
					ch <- json.RawMessage(payload)
				} else {
					c.log.Debug("no pending handler for response", "reqID", envelope.RequestID)
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
