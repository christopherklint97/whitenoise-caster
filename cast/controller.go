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

type TimerAction string

const (
	TimerActionStop   TimerAction = "stop"
	TimerActionVolume TimerAction = "volume"
)

type TimerInfo struct {
	Active      bool        `json:"active"`
	RemainingS  int         `json:"remaining_s"`
	Action      TimerAction `json:"action"`
	VolumeLevel float32     `json:"volume_level"`
}

type Status struct {
	State       State     `json:"state"`
	SpeakerIP   string    `json:"speaker_ip,omitempty"`
	SpeakerName string    `json:"speaker_name,omitempty"`
	Error       string    `json:"error,omitempty"`
	Timer       TimerInfo `json:"timer"`
}

// castClient is the subset of Client methods the controller uses.
// *Client satisfies this interface; tests can substitute a mock.
type castClient interface {
	LoadMedia(ctx context.Context, url, contentType string) error
	SetMuted(ctx context.Context, muted bool) error
	SetVolume(ctx context.Context, level float32) error
	Play(ctx context.Context) error
	Pause(ctx context.Context) error
	GetMediaStatus(ctx context.Context) (*mediaStatus, error)
	Close()
}

type Controller struct {
	mu       sync.RWMutex
	log      *slog.Logger
	audioURL string

	client     castClient
	status     Status
	cancelLoop context.CancelFunc

	// dialAndLaunch connects to a speaker and launches the media receiver.
	// Defaults to connectAndLaunch; overridden in tests.
	dialAndLaunch func(ctx context.Context, ip string) (castClient, error)

	// reconnectDelays is the backoff schedule for reconnect retries.
	reconnectDelays []time.Duration

	timerCancel   context.CancelFunc
	timerDeadline time.Time
	timerAction   TimerAction
	timerVolume   float32
}

func NewController(logger *slog.Logger, audioURL string) *Controller {
	c := &Controller{
		log:             logger,
		audioURL:        audioURL,
		status:          Status{State: StateDisconnected},
		reconnectDelays: []time.Duration{0, 5 * time.Second, 10 * time.Second},
	}
	c.dialAndLaunch = c.connectAndLaunch
	return c
}

const connectTimeout = 10 * time.Second

func (c *Controller) Play(ctx context.Context, speakerIP, speakerName string) error {
	c.mu.Lock()

	// Stop any existing session
	c.stopLocked()

	c.status = Status{
		State:       StateConnecting,
		SpeakerIP:   speakerIP,
		SpeakerName: speakerName,
	}

	// Unlock during slow network I/O so GetStatus and other reads aren't blocked.
	c.mu.Unlock()

	client, err := c.dialAndLaunch(ctx, speakerIP)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err != nil {
		c.status = Status{State: StateError, Error: fmt.Sprintf("connect: %v", err)}
		return fmt.Errorf("connecting to %s (%s): %w", speakerName, speakerIP, err)
	}

	c.client = client

	if err := c.loadMedia(ctx); err != nil {
		_ = c.client.SetMuted(ctx, false)
		c.client.Close()
		c.client = nil
		c.status = Status{State: StateError, Error: fmt.Sprintf("load: %v", err)}
		return fmt.Errorf("loading media: %w", err)
	}

	// Unmute now that the launch chime has passed.
	if err := c.client.SetMuted(ctx, false); err != nil {
		c.log.Warn("failed to unmute after launch", "error", err)
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

func (c *Controller) connectAndLaunch(ctx context.Context, ip string) (castClient, error) {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client, err := Connect(connCtx, ip, 8009, connectTimeout, c.log)
	if err != nil {
		return nil, err
	}

	// Mute to suppress the launch chime.
	if err := client.SetMuted(connCtx, true); err != nil {
		c.log.Warn("failed to mute before launch", "error", err)
	}

	if err := client.LaunchMediaReceiver(connCtx); err != nil {
		_ = client.SetMuted(connCtx, false)
		client.Close()
		return nil, fmt.Errorf("launch media receiver: %w", err)
	}

	return client, nil
}

func (c *Controller) Pause() error {
	c.mu.Lock()

	if c.client == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch c.status.State {
	case StatePlaying:
		c.log.Info("pausing playback", "speaker", c.status.SpeakerName)
		if err := c.client.Pause(ctx); err != nil {
			c.mu.Unlock()
			return fmt.Errorf("pausing: %w", err)
		}
		c.status.State = StatePaused
		c.log.Info("paused")
		c.mu.Unlock()
		return nil
	case StatePaused:
		speakerIP := c.status.SpeakerIP
		speakerName := c.status.SpeakerName
		c.log.Info("resuming playback", "speaker", speakerName)
		if err := c.client.Play(ctx); err != nil {
			// Session is stale (e.g., device killed the app during idle).
			// Clean up and start a fresh session to the same speaker.
			c.log.Warn("resume failed, attempting fresh play", "error", err)
			c.stopLocked()
			c.mu.Unlock()
			playCtx, playCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer playCancel()
			return c.Play(playCtx, speakerIP, speakerName)
		}
		c.status.State = StatePlaying
		c.log.Info("resumed")
		c.mu.Unlock()
		return nil
	default:
		state := c.status.State
		c.mu.Unlock()
		return fmt.Errorf("cannot toggle pause in state: %s", state)
	}
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
	c.cancelTimerLocked()
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
	s := c.status
	if c.timerCancel != nil {
		remaining := int(time.Until(c.timerDeadline).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		s.Timer = TimerInfo{
			Active:      true,
			RemainingS:  remaining,
			Action:      c.timerAction,
			VolumeLevel: c.timerVolume,
		}
	}
	return s
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

func (c *Controller) SetTimer(durationS int, action TimerAction, volumeLevel float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	c.cancelTimerLocked()

	dur := time.Duration(durationS) * time.Second
	c.timerDeadline = time.Now().Add(dur)
	c.timerAction = action
	c.timerVolume = volumeLevel

	ctx, cancel := context.WithCancel(context.Background())
	c.timerCancel = cancel
	go c.timerLoop(ctx, dur, action, volumeLevel)

	c.log.Info("timer set", "duration_s", durationS, "action", action)
	return nil
}

func (c *Controller) CancelTimer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelTimerLocked()
}

func (c *Controller) cancelTimerLocked() {
	if c.timerCancel != nil {
		c.timerCancel()
		c.timerCancel = nil
	}
	c.timerDeadline = time.Time{}
	c.timerAction = ""
	c.timerVolume = 0
}

func (c *Controller) timerLoop(ctx context.Context, dur time.Duration, action TimerAction, volumeLevel float32) {
	t := time.NewTimer(dur)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return
	case <-t.C:
	}

	c.mu.Lock()

	// Verify the timer wasn't cancelled while we were waiting.
	if c.timerCancel == nil {
		c.mu.Unlock()
		return
	}

	c.log.Info("timer fired", "action", action)

	switch action {
	case TimerActionStop:
		c.stopLocked()
		c.mu.Unlock()
	case TimerActionVolume:
		client := c.client
		// Clear timer state before releasing lock.
		c.cancelTimerLocked()
		c.mu.Unlock()

		if client != nil {
			volCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.SetVolume(volCtx, volumeLevel); err != nil {
				c.log.Error("timer volume set failed", "error", err)
			} else {
				c.log.Info("timer volume set", "level", volumeLevel)
			}
		}
	default:
		c.cancelTimerLocked()
		c.mu.Unlock()
	}
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

			// Grab the client reference then release the lock during slow
			// network I/O so Stop() isn't blocked while we poll.
			client := c.client
			c.mu.Unlock()

			pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
			ms, err := client.GetMediaStatus(pollCtx)
			pollCancel()

			// Re-acquire the lock and verify the session is still ours.
			// Stop() may have run while we were polling.
			c.mu.Lock()

			if c.client != client {
				// Session was stopped or replaced while we were polling.
				c.mu.Unlock()
				return
			}

			if err != nil {
				consecutiveErrors++
				c.log.Error("status poll failed", "error", err, "consecutive", consecutiveErrors)
				if consecutiveErrors >= 3 {
					c.log.Error("too many consecutive errors, attempting full reconnect")
					c.reconnect(ctx, speakerIP, speakerName)
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

// reconnect drops the lock during slow I/O, then re-acquires it.
// Caller must hold c.mu and must NOT use defer c.mu.Unlock() before calling this.
func (c *Controller) reconnect(ctx context.Context, speakerIP, speakerName string) {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}

	var lastErr error

	for attempt, delay := range c.reconnectDelays {
		if delay > 0 {
			c.log.Info("reconnect backoff", "delay", delay, "attempt", attempt+1)
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				c.mu.Lock()
				return
			case <-time.After(delay):
			}
			c.mu.Lock()
		}

		c.log.Info("reconnecting", "speaker", speakerName, "ip", speakerIP, "attempt", attempt+1)

		// Drop lock during slow network I/O.
		c.mu.Unlock()
		client, err := c.dialAndLaunch(ctx, speakerIP)
		c.mu.Lock()

		if err != nil {
			lastErr = err
			c.log.Error("reconnect failed", "error", err, "attempt", attempt+1)
			continue
		}

		c.client = client

		loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := c.loadMedia(loadCtx); err != nil {
			c.log.Error("reconnect load failed", "error", err, "attempt", attempt+1)
			_ = c.client.SetMuted(loadCtx, false)
			loadCancel()
			c.client.Close()
			c.client = nil
			lastErr = err
			continue
		}

		// Unmute now that the launch chime has passed.
		if err := c.client.SetMuted(loadCtx, false); err != nil {
			c.log.Warn("failed to unmute after reconnect", "error", err)
		}
		loadCancel()

		c.status = Status{
			State:       StatePlaying,
			SpeakerIP:   speakerIP,
			SpeakerName: speakerName,
		}
		c.log.Info("reconnected successfully", "speaker", speakerName, "attempt", attempt+1)
		return
	}

	// All retries exhausted — cancel timer and enter error state.
	c.cancelTimerLocked()
	c.status = Status{
		State:       StateError,
		SpeakerIP:   speakerIP,
		SpeakerName: speakerName,
		Error:       fmt.Sprintf("reconnect: %v", lastErr),
	}
}
