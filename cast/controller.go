package cast

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vishen/go-chromecast/application"
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

	app        *application.Application
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

	app := application.NewApplication(
		application.WithDebug(false),
		application.WithCacheDisabled(true),
	)

	if err := app.Start(speakerIP, 8009); err != nil {
		c.status = Status{State: StateError, Error: fmt.Sprintf("connect: %v", err)}
		return fmt.Errorf("connecting to %s (%s): %w", speakerName, speakerIP, err)
	}

	c.app = app

	if err := c.loadMedia(); err != nil {
		_ = c.app.Close(false)
		c.app = nil
		c.status = Status{State: StateError, Error: fmt.Sprintf("load: %v", err)}
		return fmt.Errorf("loading media: %w", err)
	}

	c.status = Status{
		State:       StatePlaying,
		SpeakerIP:   speakerIP,
		SpeakerName: speakerName,
	}

	loopCtx, cancel := context.WithCancel(ctx)
	c.cancelLoop = cancel
	go c.monitorLoop(loopCtx, speakerIP, speakerName)

	c.log.Info("playback started", "speaker", speakerName, "ip", speakerIP)
	return nil
}

func (c *Controller) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.app == nil {
		return fmt.Errorf("not connected")
	}

	switch c.status.State {
	case StatePlaying:
		if err := c.app.Pause(); err != nil {
			return fmt.Errorf("pausing: %w", err)
		}
		c.status.State = StatePaused
	case StatePaused:
		if err := c.app.Unpause(); err != nil {
			return fmt.Errorf("unpausing: %w", err)
		}
		c.status.State = StatePlaying
	default:
		return fmt.Errorf("cannot toggle pause in state: %s", c.status.State)
	}

	return nil
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.app == nil {
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
	if c.app != nil {
		_ = c.app.StopMedia()
		_ = c.app.Close(false)
		c.app = nil
	}
	c.status = Status{State: StateDisconnected}
}

func (c *Controller) GetStatus() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *Controller) loadMedia() error {
	return c.app.Load(c.audioURL, 0, "audio/mpeg", false, false, false)
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

			if c.app == nil {
				c.mu.Unlock()
				return
			}

			castApp, castMedia, _ := c.app.Status()

			// Check if media has finished (need to re-load for looping)
			if castMedia == nil || castMedia.PlayerState == "IDLE" {
				// Media finished or was never started — re-load for looping
				c.log.Info("media idle/finished, re-loading for loop")
				if err := c.loadMedia(); err != nil {
					consecutiveErrors++
					c.log.Error("re-load failed", "error", err, "consecutive", consecutiveErrors)
					if consecutiveErrors >= 3 {
						c.log.Error("too many consecutive errors, attempting full reconnect")
						c.reconnectLocked(ctx, speakerIP, speakerName)
						consecutiveErrors = 0
					}
				} else {
					consecutiveErrors = 0
					c.status.State = StatePlaying
				}
			} else if castApp == nil {
				// App disappeared — reconnect
				consecutiveErrors++
				c.log.Warn("cast app disappeared", "consecutive", consecutiveErrors)
				if consecutiveErrors >= 3 {
					c.reconnectLocked(ctx, speakerIP, speakerName)
					consecutiveErrors = 0
				}
			} else {
				consecutiveErrors = 0
				// Sync state from actual player state
				switch castMedia.PlayerState {
				case "PLAYING":
					c.status.State = StatePlaying
				case "PAUSED":
					c.status.State = StatePaused
				case "BUFFERING":
					c.status.State = StatePlaying
				}
			}

			c.mu.Unlock()
		}
	}
}

func (c *Controller) reconnectLocked(ctx context.Context, speakerIP, speakerName string) {
	if c.app != nil {
		_ = c.app.Close(false)
		c.app = nil
	}

	c.log.Info("reconnecting", "speaker", speakerName, "ip", speakerIP)

	app := application.NewApplication(
		application.WithDebug(false),
		application.WithCacheDisabled(true),
	)

	if err := app.Start(speakerIP, 8009); err != nil {
		c.log.Error("reconnect failed", "error", err)
		c.status = Status{
			State:       StateError,
			SpeakerIP:   speakerIP,
			SpeakerName: speakerName,
			Error:       fmt.Sprintf("reconnect: %v", err),
		}
		return
	}

	c.app = app

	if err := c.loadMedia(); err != nil {
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
