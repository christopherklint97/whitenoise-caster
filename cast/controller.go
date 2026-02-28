package cast

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StatePlaying      State = "playing"
	StatePaused       State = "paused"
	StateError        State = "error"
)

type Status struct {
	State       State  `json:"state"`
	SpeakerIP   string `json:"speaker_ip,omitempty"`
	SpeakerName string `json:"speaker_name,omitempty"`
	Error       string `json:"error,omitempty"`
}

type Controller struct {
	mu       sync.RWMutex
	log      *slog.Logger
	audioURL string

	client     *Client
	status     Status
	cancelLoop context.CancelFunc
}

func NewController(logger *slog.Logger, audioURL string) *Controller {
	return &Controller{
		log:      logger,
		audioURL: audioURL,
		status:   Status{State: StateDisconnected},
	}
}

const connectTimeout = 10 * time.Second

func (c *Controller) Play(ctx context.Context, speakerIP, speakerName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Stop any existing session
	c.stopLocked()

	c.status = Status{
		State:       StateConnecting,
		SpeakerIP:   speakerIP,
		SpeakerName: speakerName,
	}

	client, err := c.connectAndLaunch(ctx, speakerIP)
	if err != nil {
		c.status = Status{State: StateError, Error: fmt.Sprintf("connect: %v", err)}
		return fmt.Errorf("connecting to %s (%s): %w", speakerName, speakerIP, err)
	}

	c.client = client

	if err := c.loadMedia(ctx); err != nil {
		c.client.Close()
		c.client = nil
		c.status = Status{State: StateError, Error: fmt.Sprintf("load: %v", err)}
		return fmt.Errorf("loading media: %w", err)
	}

	c.status = Status{
		State:       StatePlaying,
		SpeakerIP:   speakerIP,
		SpeakerName: speakerName,
	}

	// Use background context so the monitor loop outlives the HTTP request.
	loopCtx, cancel := context.WithCancel(context.Background())
	c.cancelLoop = cancel
	go c.monitorLoop(loopCtx, speakerIP, speakerName)

	c.log.Info("playback started", "speaker", speakerName, "ip", speakerIP)
	return nil
}

func (c *Controller) connectAndLaunch(ctx context.Context, ip string) (*Client, error) {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client, err := Connect(connCtx, ip, 8009, connectTimeout, c.log)
	if err != nil {
		return nil, err
	}

	if err := client.LaunchMediaReceiver(connCtx); err != nil {
		client.Close()
		return nil, fmt.Errorf("launch media receiver: %w", err)
	}

	return client, nil
}

func (c *Controller) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch c.status.State {
	case StatePlaying:
		c.log.Info("pausing playback", "speaker", c.status.SpeakerName)
		if err := c.client.Pause(ctx); err != nil {
			return fmt.Errorf("pausing: %w", err)
		}
		c.status.State = StatePaused
		c.log.Info("paused")
	case StatePaused:
		c.log.Info("resuming playback", "speaker", c.status.SpeakerName)
		if err := c.client.Play(ctx); err != nil {
			return fmt.Errorf("resuming: %w", err)
		}
		c.status.State = StatePlaying
		c.log.Info("resumed")
	default:
		return fmt.Errorf("cannot toggle pause in state: %s", c.status.State)
	}

	return nil
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil
	}

	c.stopLocked()
	c.log.Info("playback stopped")
	return nil
}

func (c *Controller) stopLocked() {
	if c.cancelLoop != nil {
		c.cancelLoop()
		c.cancelLoop = nil
	}
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	c.status = Status{State: StateDisconnected}
}

func (c *Controller) GetStatus() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Controller) SetVolume(level float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.client.SetVolume(ctx, level); err != nil {
		return fmt.Errorf("setting volume: %w", err)
	}

	c.log.Info("volume set", "level", level)
	return nil
}

func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *Controller) loadMedia(ctx context.Context) error {
	return c.client.LoadMedia(ctx, c.audioURL, "audio/mpeg")
}

func (c *Controller) monitorLoop(ctx context.Context, speakerIP, speakerName string) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()

			if c.client == nil {
				c.mu.Unlock()
				return
			}

			if c.status.State == StatePaused {
				c.mu.Unlock()
				continue
			}

			pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
			ms, err := c.client.GetMediaStatus(pollCtx)
			pollCancel()

			if err != nil {
				consecutiveErrors++
				c.log.Error("status poll failed", "error", err, "consecutive", consecutiveErrors)
				if consecutiveErrors >= 3 {
					c.log.Error("too many consecutive errors, attempting full reconnect")
					c.reconnectLocked(ctx, speakerIP, speakerName)
					consecutiveErrors = 0
				}
				c.mu.Unlock()
				continue
			}

			consecutiveErrors = 0

			switch ms.PlayerState {
			case "IDLE", "":
				c.log.Info("media idle/finished, re-loading for loop")
				loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Second)
				if err := c.loadMedia(loadCtx); err != nil {
					c.log.Error("re-load failed", "error", err)
				} else {
					c.status.State = StatePlaying
				}
				loadCancel()
			case "PLAYING":
				c.status.State = StatePlaying
			case "PAUSED":
				c.status.State = StatePaused
			case "BUFFERING":
				c.status.State = StatePlaying
			}

			c.mu.Unlock()
		}
	}
}

func (c *Controller) reconnectLocked(ctx context.Context, speakerIP, speakerName string) {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}

	c.log.Info("reconnecting", "speaker", speakerName, "ip", speakerIP)

	client, err := c.connectAndLaunch(ctx, speakerIP)
	if err != nil {
		c.log.Error("reconnect failed", "error", err)
		c.status = Status{
			State:       StateError,
			SpeakerIP:   speakerIP,
			SpeakerName: speakerName,
			Error:       fmt.Sprintf("reconnect: %v", err),
		}
		return
	}

	c.client = client

	loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Second)
	defer loadCancel()

	if err := c.loadMedia(loadCtx); err != nil {
		c.log.Error("reconnect load failed", "error", err)
		c.status = Status{
			State:       StateError,
			SpeakerIP:   speakerIP,
			SpeakerName: speakerName,
			Error:       fmt.Sprintf("reconnect load: %v", err),
		}
		return
	}

	c.status = Status{
		State:       StatePlaying,
		SpeakerIP:   speakerIP,
		SpeakerName: speakerName,
	}
	c.log.Info("reconnected successfully", "speaker", speakerName)
}
