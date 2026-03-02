package cast

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// mockClient implements castClient for testing.
type mockClient struct {
	loadMediaFunc    func(ctx context.Context, url, contentType string) error
	setMutedFunc     func(ctx context.Context, muted bool) error
	setVolumeFunc    func(ctx context.Context, level float32) error
	playFunc         func(ctx context.Context) error
	pauseFunc        func(ctx context.Context) error
	getMediaStatFunc func(ctx context.Context) (*mediaStatus, error)
	closeFunc        func()

	closeCalled atomic.Int32
}

func (m *mockClient) LoadMedia(ctx context.Context, url, contentType string) error {
	if m.loadMediaFunc != nil {
		return m.loadMediaFunc(ctx, url, contentType)
	}
	return nil
}

func (m *mockClient) SetMuted(ctx context.Context, muted bool) error {
	if m.setMutedFunc != nil {
		return m.setMutedFunc(ctx, muted)
	}
	return nil
}

func (m *mockClient) SetVolume(ctx context.Context, level float32) error {
	if m.setVolumeFunc != nil {
		return m.setVolumeFunc(ctx, level)
	}
	return nil
}

func (m *mockClient) Play(ctx context.Context) error {
	if m.playFunc != nil {
		return m.playFunc(ctx)
	}
	return nil
}

func (m *mockClient) Pause(ctx context.Context) error {
	if m.pauseFunc != nil {
		return m.pauseFunc(ctx)
	}
	return nil
}

func (m *mockClient) GetMediaStatus(ctx context.Context) (*mediaStatus, error) {
	if m.getMediaStatFunc != nil {
		return m.getMediaStatFunc(ctx)
	}
	return &mediaStatus{PlayerState: "PLAYING"}, nil
}

func (m *mockClient) Close() {
	m.closeCalled.Add(1)
	if m.closeFunc != nil {
		m.closeFunc()
	}
}

// newTestController creates a Controller with zero-delay reconnects and a
// custom dialAndLaunch function for testing.
func newTestController(dial func(ctx context.Context, ip string) (castClient, error)) *Controller {
	c := &Controller{
		log:             slog.Default(),
		audioURL:        "http://test/audio.mp3",
		status:          Status{State: StateDisconnected},
		reconnectDelays: []time.Duration{0, 0, 0},
	}
	c.dialAndLaunch = dial
	return c
}

func TestReconnect_SucceedsFirstAttempt(t *testing.T) {
	mock := &mockClient{}
	dialCalls := 0
	c := newTestController(func(ctx context.Context, ip string) (castClient, error) {
		dialCalls++
		return mock, nil
	})

	c.mu.Lock()
	c.reconnect(context.Background(), "192.168.1.1", "Living Room")
	status := c.status
	client := c.client
	c.mu.Unlock()

	if dialCalls != 1 {
		t.Fatalf("expected 1 dial call, got %d", dialCalls)
	}
	if status.State != StatePlaying {
		t.Fatalf("expected state %q, got %q", StatePlaying, status.State)
	}
	if status.SpeakerIP != "192.168.1.1" {
		t.Fatalf("expected speaker IP 192.168.1.1, got %q", status.SpeakerIP)
	}
	if client != mock {
		t.Fatal("expected client to be set to mock")
	}
}

func TestReconnect_SucceedsOnRetry(t *testing.T) {
	mock := &mockClient{}
	dialCalls := 0
	c := newTestController(func(ctx context.Context, ip string) (castClient, error) {
		dialCalls++
		if dialCalls == 1 {
			return nil, fmt.Errorf("connection refused")
		}
		return mock, nil
	})

	c.mu.Lock()
	c.reconnect(context.Background(), "192.168.1.1", "Living Room")
	status := c.status
	client := c.client
	c.mu.Unlock()

	if dialCalls != 2 {
		t.Fatalf("expected 2 dial calls, got %d", dialCalls)
	}
	if status.State != StatePlaying {
		t.Fatalf("expected state %q, got %q", StatePlaying, status.State)
	}
	if client != mock {
		t.Fatal("expected client to be set to mock")
	}
}

func TestReconnect_AllAttemptsFail(t *testing.T) {
	dialCalls := 0
	c := newTestController(func(ctx context.Context, ip string) (castClient, error) {
		dialCalls++
		return nil, fmt.Errorf("dial attempt %d failed", dialCalls)
	})

	c.mu.Lock()
	c.reconnect(context.Background(), "192.168.1.1", "Living Room")
	status := c.status
	client := c.client
	c.mu.Unlock()

	if dialCalls != 3 {
		t.Fatalf("expected 3 dial calls, got %d", dialCalls)
	}
	if status.State != StateError {
		t.Fatalf("expected state %q, got %q", StateError, status.State)
	}
	if status.Error == "" {
		t.Fatal("expected non-empty error message")
	}
	if client != nil {
		t.Fatal("expected client to be nil after all attempts fail")
	}
}

func TestReconnect_CancelsTimerOnFailure(t *testing.T) {
	c := newTestController(func(ctx context.Context, ip string) (castClient, error) {
		return nil, fmt.Errorf("dial failed")
	})

	// Simulate an active timer by setting a cancel func.
	timerCtx, timerCancel := context.WithCancel(context.Background())
	defer timerCancel()

	c.mu.Lock()
	c.timerCancel = timerCancel
	c.timerDeadline = time.Now().Add(5 * time.Minute)
	c.timerAction = TimerActionStop
	c.mu.Unlock()

	c.mu.Lock()
	c.reconnect(context.Background(), "192.168.1.1", "Living Room")
	hasTimer := c.timerCancel != nil
	action := c.timerAction
	c.mu.Unlock()

	if hasTimer {
		t.Fatal("expected timer to be cancelled after all reconnect attempts fail")
	}
	if action != "" {
		t.Fatalf("expected timer action to be cleared, got %q", action)
	}

	// The timer context should be cancelled.
	select {
	case <-timerCtx.Done():
	default:
		t.Fatal("expected timer context to be done")
	}
}

func TestReconnect_CleansUpClientOnLoadFailure(t *testing.T) {
	mock1 := &mockClient{
		loadMediaFunc: func(ctx context.Context, url, contentType string) error {
			return fmt.Errorf("load failed")
		},
	}
	mock2 := &mockClient{
		loadMediaFunc: func(ctx context.Context, url, contentType string) error {
			return fmt.Errorf("load failed again")
		},
	}
	mock3 := &mockClient{
		loadMediaFunc: func(ctx context.Context, url, contentType string) error {
			return fmt.Errorf("load failed a third time")
		},
	}

	dialCalls := 0
	mocks := []castClient{mock1, mock2, mock3}
	c := newTestController(func(ctx context.Context, ip string) (castClient, error) {
		m := mocks[dialCalls]
		dialCalls++
		return m, nil
	})

	c.mu.Lock()
	c.reconnect(context.Background(), "192.168.1.1", "Living Room")
	status := c.status
	client := c.client
	c.mu.Unlock()

	// All 3 dials succeeded but all 3 loads failed.
	if dialCalls != 3 {
		t.Fatalf("expected 3 dial calls, got %d", dialCalls)
	}

	// Each client should have been closed after its load failure.
	if n := mock1.closeCalled.Load(); n != 1 {
		t.Fatalf("expected mock1.Close called 1 time, got %d", n)
	}
	if n := mock2.closeCalled.Load(); n != 1 {
		t.Fatalf("expected mock2.Close called 1 time, got %d", n)
	}
	if n := mock3.closeCalled.Load(); n != 1 {
		t.Fatalf("expected mock3.Close called 1 time, got %d", n)
	}

	if status.State != StateError {
		t.Fatalf("expected state %q, got %q", StateError, status.State)
	}
	if client != nil {
		t.Fatal("expected client to be nil after all load failures")
	}
}

func TestReconnect_AbortsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dialCalls := 0

	c := newTestController(func(_ context.Context, ip string) (castClient, error) {
		dialCalls++
		return nil, fmt.Errorf("dial failed")
	})
	// Use real delays so the backoff select blocks long enough for cancellation.
	c.reconnectDelays = []time.Duration{0, 5 * time.Second, 5 * time.Second}

	done := make(chan struct{})
	go func() {
		c.mu.Lock()
		c.reconnect(ctx, "192.168.1.1", "Living Room")
		c.mu.Unlock()
		close(done)
	}()

	// Let the first attempt fail, then cancel during the backoff wait.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect did not abort after context cancellation")
	}

	c.mu.RLock()
	status := c.status
	c.mu.RUnlock()

	// Context cancel during backoff should return early without setting error state.
	if status.State == StateError {
		t.Fatal("expected state NOT to be error after context cancellation")
	}
	if dialCalls > 2 {
		t.Fatalf("expected at most 2 dial calls before abort, got %d", dialCalls)
	}
}
