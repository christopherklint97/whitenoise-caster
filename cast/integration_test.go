//go:build integration

package cast

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

const (
	livingRoomIP   = "192.168.0.64"
	livingRoomName = "Living Room"
	bedroomIP      = "192.168.0.151"
	bedroomName    = "Bedroom"
)

func audioURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("INTEGRATION_AUDIO_URL")
	if u == "" {
		t.Fatal("INTEGRATION_AUDIO_URL must be set (e.g. via .env)")
	}
	return u
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestIntegration_Connect(t *testing.T) {
	log := testLogger(t)

	for _, sp := range []struct{ ip, name string }{
		{livingRoomIP, livingRoomName},
		{bedroomIP, bedroomName},
	} {
		t.Run(sp.name, func(t *testing.T) {
			subCtx, subCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer subCancel()

			t.Logf("[%s] starting Connect to %s:%d (timeout=%s)", sp.name, sp.ip, 8009, connectTimeout)
			start := time.Now()

			client, err := Connect(subCtx, sp.ip, 8009, connectTimeout, log)
			elapsed := time.Since(start)

			if err != nil {
				t.Fatalf("[%s] Connect failed after %s: %v", sp.name, elapsed, err)
			}
			t.Logf("[%s] Connect succeeded in %s", sp.name, elapsed)
			t.Cleanup(func() {
				t.Logf("[%s] closing client", sp.name)
				client.Close()
				t.Logf("[%s] client closed", sp.name)
			})
		})
	}
}

func TestIntegration_LaunchMediaReceiver(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)

	t.Logf("dialing %s:%d...", livingRoomIP, 8009)
	start := time.Now()
	client, err := Connect(ctx, livingRoomIP, 8009, connectTimeout, log)
	if err != nil {
		t.Fatalf("Connect failed after %s: %v", time.Since(start), err)
	}
	t.Logf("connected in %s", time.Since(start))
	t.Cleanup(func() {
		t.Logf("closing client")
		client.Close()
		t.Logf("client closed")
	})

	t.Logf("launching media receiver...")
	start = time.Now()
	if err := client.LaunchMediaReceiver(ctx); err != nil {
		t.Fatalf("LaunchMediaReceiver failed after %s: %v", time.Since(start), err)
	}
	t.Logf("media receiver launched in %s", time.Since(start))

	client.mu.Lock()
	tid := client.transportID
	client.mu.Unlock()

	if tid == "" {
		t.Fatal("transportID is empty after LaunchMediaReceiver")
	}
	t.Logf("transportID=%s", tid)
}

func TestIntegration_LoadAndPlay(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)
	url := audioURL(t)

	t.Logf("creating controller with audioURL=%s", url)
	ctrl := NewController(log, url)
	t.Cleanup(func() {
		t.Logf("closing controller")
		ctrl.Close()
		t.Logf("controller closed")
	})

	t.Logf("calling Play on %s (%s)...", livingRoomName, livingRoomIP)
	start := time.Now()
	if err := ctrl.Play(ctx, livingRoomIP, livingRoomName); err != nil {
		t.Fatalf("Play failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play returned in %s", time.Since(start))

	status := ctrl.GetStatus()
	t.Logf("status after Play: state=%s speaker=%s", status.State, status.SpeakerName)
	if status.State != StatePlaying {
		t.Fatalf("state after Play: want %q, got %q", StatePlaying, status.State)
	}

	t.Logf("sleeping 3s to let playback settle...")
	time.Sleep(3 * time.Second)

	ctrl.mu.RLock()
	client := ctrl.client
	ctrl.mu.RUnlock()

	if client == nil {
		t.Fatal("client is nil after Play")
	}

	t.Logf("polling GetMediaStatus...")
	start = time.Now()
	ms, err := client.GetMediaStatus(ctx)
	if err != nil {
		t.Fatalf("GetMediaStatus failed after %s: %v", time.Since(start), err)
	}
	t.Logf("GetMediaStatus returned in %s: playerState=%s", time.Since(start), ms.PlayerState)
	if ms.PlayerState != "PLAYING" {
		t.Errorf("media player state: want PLAYING, got %q", ms.PlayerState)
	}

	t.Logf("confirmed PLAYING on %s", livingRoomName)
}

func TestIntegration_PauseResume(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)

	t.Logf("creating controller and starting playback on %s...", livingRoomName)
	ctrl := NewController(log, audioURL(t))
	t.Cleanup(func() {
		t.Logf("closing controller")
		ctrl.Close()
		t.Logf("controller closed")
	})

	start := time.Now()
	if err := ctrl.Play(ctx, livingRoomIP, livingRoomName); err != nil {
		t.Fatalf("Play failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play returned in %s", time.Since(start))
	time.Sleep(2 * time.Second)

	// Pause
	t.Logf("pausing...")
	start = time.Now()
	if err := ctrl.Pause(); err != nil {
		t.Fatalf("Pause failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Pause returned in %s, state=%s", time.Since(start), ctrl.GetStatus().State)
	if s := ctrl.GetStatus().State; s != StatePaused {
		t.Fatalf("state after Pause: want %q, got %q", StatePaused, s)
	}

	ctrl.mu.RLock()
	client := ctrl.client
	ctrl.mu.RUnlock()

	t.Logf("polling GetMediaStatus after pause...")
	start = time.Now()
	ms, err := client.GetMediaStatus(ctx)
	if err != nil {
		t.Fatalf("GetMediaStatus after pause failed after %s: %v", time.Since(start), err)
	}
	t.Logf("GetMediaStatus: playerState=%s (took %s)", ms.PlayerState, time.Since(start))
	if ms.PlayerState != "PAUSED" {
		t.Errorf("media state after pause: want PAUSED, got %q", ms.PlayerState)
	}

	// Resume (Pause toggles)
	t.Logf("resuming...")
	start = time.Now()
	if err := ctrl.Pause(); err != nil {
		t.Fatalf("Resume failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Resume returned in %s, state=%s", time.Since(start), ctrl.GetStatus().State)
	if s := ctrl.GetStatus().State; s != StatePlaying {
		t.Fatalf("state after Resume: want %q, got %q", StatePlaying, s)
	}

	time.Sleep(1 * time.Second)

	t.Logf("polling GetMediaStatus after resume...")
	start = time.Now()
	ms, err = client.GetMediaStatus(ctx)
	if err != nil {
		t.Fatalf("GetMediaStatus after resume failed after %s: %v", time.Since(start), err)
	}
	t.Logf("GetMediaStatus: playerState=%s (took %s)", ms.PlayerState, time.Since(start))
	if ms.PlayerState != "PLAYING" {
		t.Errorf("media state after resume: want PLAYING, got %q", ms.PlayerState)
	}

	t.Logf("pause/resume verified on %s", livingRoomName)
}

func TestIntegration_SetVolume(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)

	t.Logf("creating controller and starting playback on %s...", livingRoomName)
	ctrl := NewController(log, audioURL(t))
	t.Cleanup(func() {
		t.Logf("closing controller")
		ctrl.Close()
		t.Logf("controller closed")
	})

	start := time.Now()
	if err := ctrl.Play(ctx, livingRoomIP, livingRoomName); err != nil {
		t.Fatalf("Play failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play returned in %s", time.Since(start))
	time.Sleep(2 * time.Second)

	t.Logf("setting volume to 0.1...")
	start = time.Now()
	if err := ctrl.SetVolume(0.1); err != nil {
		t.Fatalf("SetVolume(0.1) failed after %s: %v", time.Since(start), err)
	}
	t.Logf("SetVolume returned in %s", time.Since(start))
}

func TestIntegration_StopCleansUp(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)

	t.Logf("creating controller and starting playback on %s...", livingRoomName)
	ctrl := NewController(log, audioURL(t))
	t.Cleanup(func() {
		t.Logf("closing controller")
		ctrl.Close()
		t.Logf("controller closed")
	})

	start := time.Now()
	if err := ctrl.Play(ctx, livingRoomIP, livingRoomName); err != nil {
		t.Fatalf("Play failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play returned in %s", time.Since(start))
	time.Sleep(2 * time.Second)

	t.Logf("stopping...")
	start = time.Now()
	if err := ctrl.Stop(); err != nil {
		t.Fatalf("Stop failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Stop returned in %s", time.Since(start))

	status := ctrl.GetStatus()
	t.Logf("status after Stop: state=%s", status.State)
	if status.State != StateDisconnected {
		t.Errorf("state after Stop: want %q, got %q", StateDisconnected, status.State)
	}

	ctrl.mu.RLock()
	client := ctrl.client
	ctrl.mu.RUnlock()

	if client != nil {
		t.Error("client should be nil after Stop")
	}

	t.Logf("stop cleanup verified")
}

func TestIntegration_SwitchSpeaker(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)

	ctrl := NewController(log, audioURL(t))
	t.Cleanup(func() {
		t.Logf("closing controller")
		ctrl.Close()
		t.Logf("controller closed")
	})

	// Play on Living Room first
	t.Logf("playing on %s (%s)...", livingRoomName, livingRoomIP)
	start := time.Now()
	if err := ctrl.Play(ctx, livingRoomIP, livingRoomName); err != nil {
		t.Fatalf("Play Living Room failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play Living Room returned in %s", time.Since(start))
	time.Sleep(2 * time.Second)

	s := ctrl.GetStatus()
	t.Logf("status: state=%s speaker=%s ip=%s", s.State, s.SpeakerName, s.SpeakerIP)
	if s.SpeakerIP != livingRoomIP {
		t.Fatalf("speaker IP: want %s, got %s", livingRoomIP, s.SpeakerIP)
	}

	// Switch to Bedroom (Play stops the previous session)
	t.Logf("switching to %s (%s)...", bedroomName, bedroomIP)
	start = time.Now()
	if err := ctrl.Play(ctx, bedroomIP, bedroomName); err != nil {
		t.Fatalf("Play Bedroom failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play Bedroom returned in %s", time.Since(start))
	time.Sleep(2 * time.Second)

	s = ctrl.GetStatus()
	t.Logf("status after switch: state=%s speaker=%s ip=%s", s.State, s.SpeakerName, s.SpeakerIP)
	if s.SpeakerIP != bedroomIP {
		t.Errorf("speaker IP after switch: want %s, got %s", bedroomIP, s.SpeakerIP)
	}
	if s.SpeakerName != bedroomName {
		t.Errorf("speaker name after switch: want %s, got %s", bedroomName, s.SpeakerName)
	}
	if s.State != StatePlaying {
		t.Errorf("state after switch: want %q, got %q", StatePlaying, s.State)
	}

	t.Logf("switched from %s to %s", livingRoomName, bedroomName)
}

func TestIntegration_MonitorLoopRelaunches(t *testing.T) {
	ctx := testContext(t)
	log := testLogger(t)

	t.Logf("creating controller and starting playback on %s...", livingRoomName)
	ctrl := NewController(log, audioURL(t))
	t.Cleanup(func() {
		t.Logf("closing controller")
		ctrl.Close()
		t.Logf("controller closed")
	})

	start := time.Now()
	if err := ctrl.Play(ctx, livingRoomIP, livingRoomName); err != nil {
		t.Fatalf("Play failed after %s: %v", time.Since(start), err)
	}
	t.Logf("Play returned in %s", time.Since(start))
	time.Sleep(2 * time.Second)

	// Stop media directly on the client to simulate track ending.
	ctrl.mu.RLock()
	client := ctrl.client
	ctrl.mu.RUnlock()

	if client == nil {
		t.Fatal("client is nil")
	}

	t.Logf("sending StopMedia to simulate track ending...")
	stopCtx, stopCancel := context.WithTimeout(ctx, 5*time.Second)
	defer stopCancel()
	start = time.Now()
	if err := client.StopMedia(stopCtx); err != nil {
		t.Fatalf("StopMedia failed after %s: %v", time.Since(start), err)
	}
	t.Logf("StopMedia returned in %s", time.Since(start))

	// Wait for the monitor loop (3s poll interval) to detect IDLE and re-load.
	// Give it up to 10 seconds for two poll cycles.
	t.Logf("waiting up to 10s for monitor loop to re-launch...")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		s := ctrl.GetStatus()
		t.Logf("  state=%s (elapsed=%s)", s.State, time.Since(start))
		if s.State == StatePlaying {
			t.Logf("monitor loop re-launched playback after simulated stop")
			return
		}
	}

	t.Errorf("monitor loop did not re-launch within 10s, state: %s", ctrl.GetStatus().State)
}
