package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/telnesstech/whitenoise-caster/cast"
	"github.com/telnesstech/whitenoise-caster/config"
)

// e2eMock is a stateful mock that simulates real controller state transitions.
type e2eMock struct {
	mu          sync.Mutex
	status      cast.Status
	volume      float32
	timerActive bool
	timerAction cast.TimerAction
	timerVolume float32
}

func (m *e2eMock) Play(_ context.Context, ip, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = cast.Status{
		State:       cast.StatePlaying,
		SpeakerIP:   ip,
		SpeakerName: name,
	}
	return nil
}

func (m *e2eMock) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.status.State {
	case cast.StatePlaying:
		m.status.State = cast.StatePaused
	case cast.StatePaused:
		m.status.State = cast.StatePlaying
	default:
		return fmt.Errorf("cannot toggle pause in state: %s", m.status.State)
	}
	return nil
}

func (m *e2eMock) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = cast.Status{State: cast.StateDisconnected}
	m.timerActive = false
	m.timerAction = ""
	m.timerVolume = 0
	return nil
}

func (m *e2eMock) SetVolume(level float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == cast.StateDisconnected {
		return fmt.Errorf("not connected")
	}
	m.volume = level
	return nil
}

func (m *e2eMock) SetTimer(durationS int, action cast.TimerAction, volumeLevel float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == cast.StateDisconnected {
		return fmt.Errorf("not connected")
	}
	m.timerActive = true
	m.timerAction = action
	m.timerVolume = volumeLevel
	return nil
}

func (m *e2eMock) CancelTimer() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timerActive = false
	m.timerAction = ""
	m.timerVolume = 0
}

func (m *e2eMock) GetStatus() cast.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	if m.timerActive {
		s.Timer = cast.TimerInfo{
			Active:      true,
			RemainingS:  60,
			Action:      m.timerAction,
			VolumeLevel: m.timerVolume,
		}
	}
	return s
}

// e2eEnv bundles the test server, client, and config for E2E tests.
type e2eEnv struct {
	server    *httptest.Server
	client    *http.Client
	cfg       *config.Config
	audioFile string
}

func setupE2E(t *testing.T, authUser, authPass string) *e2eEnv {
	t.Helper()

	// Temp audio file
	audioFile, err := os.CreateTemp(t.TempDir(), "whitenoise-*.mp3")
	if err != nil {
		t.Fatal(err)
	}
	audioFile.Write([]byte("fake-mp3-data-for-testing"))
	audioFile.Close()

	cfg := &config.Config{
		Speakers: []config.Speaker{
			{Name: "Living Room", IP: "192.168.1.100"},
			{Name: "Bedroom", IP: "192.168.1.101"},
		},
		AudioFile:  audioFile.Name(),
		AudioURL:   "https://example.com",
		ListenAddr: ":0",
		SecretPath: "test-secret",
	}
	cfg.Auth.Username = authUser
	cfg.Auth.Password = authPass

	mock := &e2eMock{status: cast.Status{State: cast.StateDisconnected}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	webFS := os.DirFS("../web")
	h := New(cfg, mock, logger, webFS)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &e2eEnv{
		server:    srv,
		client:    srv.Client(),
		cfg:       cfg,
		audioFile: audioFile.Name(),
	}
}

func (e *e2eEnv) url(path string) string {
	return e.server.URL + path
}

func (e *e2eEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := e.client.Get(e.url(path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) getWithAuth(t *testing.T, path, user, pass string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", e.url(path), nil)
	req.SetBasicAuth(user, pass)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) postJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp, err := e.client.Post(e.url(path), "application/json", &buf)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) postRaw(t *testing.T, path, body string) *http.Response {
	t.Helper()
	resp, err := e.client.Post(e.url(path), "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) postEmpty(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := e.client.Post(e.url(path), "", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) delete(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", e.url(path), nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func decodeStatus(t *testing.T, resp *http.Response) cast.Status {
	t.Helper()
	defer resp.Body.Close()
	var s cast.Status
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return s
}

func assertCode(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("want status %d, got %d: %s", want, resp.StatusCode, body)
	}
}

func TestE2E_FullLifecycle(t *testing.T) {
	env := setupE2E(t, "", "")

	// Initially disconnected
	resp := env.get(t, "/api/status")
	s := decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("initial state: want disconnected, got %s", s.State)
	}

	// Play Living Room
	resp = env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after play: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("speaker_name: want Living Room, got %s", s.SpeakerName)
	}

	// Status confirms playing
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("status after play: want playing, got %s", s.State)
	}

	// Stop
	resp = env.postEmpty(t, "/api/stop")
	s = decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("after stop: want disconnected, got %s", s.State)
	}

	// Status confirms disconnected
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("status after stop: want disconnected, got %s", s.State)
	}

	// Play a different speaker
	resp = env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.101"})
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after second play: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Bedroom" {
		t.Fatalf("speaker_name: want Bedroom, got %s", s.SpeakerName)
	}
}

func TestE2E_StaticFiles(t *testing.T) {
	env := setupE2E(t, "", "")

	t.Run("index.html", func(t *testing.T) {
		resp := env.get(t, "/")
		assertCode(t, resp, 200)
		ct := resp.Header.Get("Content-Type")
		if ct != "text/html; charset=utf-8" {
			t.Errorf("Content-Type: want text/html; charset=utf-8, got %s", ct)
		}
	})

	t.Run("manifest.json", func(t *testing.T) {
		resp := env.get(t, "/manifest.json")
		assertCode(t, resp, 200)
		ct := resp.Header.Get("Content-Type")
		if ct != "application/manifest+json" {
			t.Errorf("Content-Type: want application/manifest+json, got %s", ct)
		}
	})

	t.Run("icon.png", func(t *testing.T) {
		resp := env.get(t, "/icon.png")
		assertCode(t, resp, 200)
		ct := resp.Header.Get("Content-Type")
		if ct != "image/png" {
			t.Errorf("Content-Type: want image/png, got %s", ct)
		}
	})

	t.Run("nonexistent returns 404", func(t *testing.T) {
		resp := env.get(t, "/nonexistent")
		assertCode(t, resp, 404)
	})
}

func TestE2E_AudioEndpoint(t *testing.T) {
	env := setupE2E(t, "", "")

	t.Run("correct secret returns audio", func(t *testing.T) {
		resp := env.get(t, "/audio/test-secret/whitenoise.mp3")
		assertCode(t, resp, 200)
		ct := resp.Header.Get("Content-Type")
		if ct != "audio/mpeg" {
			t.Errorf("Content-Type: want audio/mpeg, got %s", ct)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "fake-mp3-data-for-testing" {
			t.Errorf("body: want fake-mp3-data-for-testing, got %s", body)
		}
	})

	t.Run("wrong secret returns 404", func(t *testing.T) {
		resp := env.get(t, "/audio/wrong-secret/whitenoise.mp3")
		assertCode(t, resp, 404)
	})

	t.Run("supports Range requests", func(t *testing.T) {
		req, _ := http.NewRequest("GET", env.url("/audio/test-secret/whitenoise.mp3"), nil)
		req.Header.Set("Range", "bytes=0-9")
		resp, err := env.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusPartialContent {
			t.Fatalf("want 206, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "fake-mp3-d" {
			t.Errorf("range body: want %q, got %q", "fake-mp3-d", body)
		}
	})
}

func TestE2E_PlayValidation(t *testing.T) {
	env := setupE2E(t, "", "")

	t.Run("unknown speaker", func(t *testing.T) {
		resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "10.0.0.1"})
		assertCode(t, resp, 400)
	})

	t.Run("empty body", func(t *testing.T) {
		resp := env.postRaw(t, "/api/play", "")
		assertCode(t, resp, 400)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		resp := env.postRaw(t, "/api/play", "{not json")
		assertCode(t, resp, 400)
	})
}

func TestE2E_PauseResumeCycle(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	s := decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after play: want playing, got %s", s.State)
	}

	// Pause — should stay connected with speaker info
	resp = env.postEmpty(t, "/api/pause")
	s = decodeStatus(t, resp)
	if s.State != cast.StatePaused {
		t.Fatalf("after pause: want paused, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("paused speaker_name: want Living Room, got %s", s.SpeakerName)
	}
	if s.SpeakerIP != "192.168.1.100" {
		t.Fatalf("paused speaker_ip: want 192.168.1.100, got %s", s.SpeakerIP)
	}

	// Status confirms paused with speaker info
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if s.State != cast.StatePaused {
		t.Fatalf("status after pause: want paused, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("status paused speaker_name: want Living Room, got %s", s.SpeakerName)
	}

	// Resume (pause again toggles back to playing)
	resp = env.postEmpty(t, "/api/pause")
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after resume: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("resumed speaker_name: want Living Room, got %s", s.SpeakerName)
	}

	// Pause while disconnected should error
	env.postEmpty(t, "/api/stop")
	resp = env.postEmpty(t, "/api/pause")
	assertCode(t, resp, 400)
}

func TestE2E_StopFromPlaying(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	s := decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after play: want playing, got %s", s.State)
	}

	// Stop
	resp = env.postEmpty(t, "/api/stop")
	assertCode(t, resp, 200)
	s = decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("after stop: want disconnected, got %s", s.State)
	}
	if s.SpeakerName != "" {
		t.Fatalf("after stop: speaker_name should be empty, got %s", s.SpeakerName)
	}

	// Status confirms disconnected
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("status after stop: want disconnected, got %s", s.State)
	}
}

func TestE2E_StopFromPaused(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play then pause
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	resp.Body.Close()
	resp = env.postEmpty(t, "/api/pause")
	s := decodeStatus(t, resp)
	if s.State != cast.StatePaused {
		t.Fatalf("after pause: want paused, got %s", s.State)
	}

	// Stop from paused
	resp = env.postEmpty(t, "/api/stop")
	assertCode(t, resp, 200)
	s = decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("after stop from paused: want disconnected, got %s", s.State)
	}
	if s.SpeakerName != "" {
		t.Fatalf("after stop: speaker_name should be empty, got %s", s.SpeakerName)
	}
}

func TestE2E_StopIdempotent(t *testing.T) {
	env := setupE2E(t, "", "")

	// Stop while already disconnected should be fine
	resp := env.postEmpty(t, "/api/stop")
	assertCode(t, resp, 200)
	s := decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("want disconnected, got %s", s.State)
	}
}

func TestE2E_Volume(t *testing.T) {
	env := setupE2E(t, "", "")

	t.Run("set volume while playing", func(t *testing.T) {
		// Start playback first
		resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
		assertCode(t, resp, 200)
		resp.Body.Close()

		// Set volume to 10%
		resp = env.postJSON(t, "/api/volume", volumeRequest{Level: 0.1})
		assertCode(t, resp, 200)
		resp.Body.Close()

		// Set volume to 20%
		resp = env.postJSON(t, "/api/volume", volumeRequest{Level: 0.2})
		assertCode(t, resp, 200)
		resp.Body.Close()

		// Cleanup
		resp = env.postEmpty(t, "/api/stop")
		resp.Body.Close()
	})

	t.Run("set volume while disconnected fails", func(t *testing.T) {
		resp := env.postJSON(t, "/api/volume", volumeRequest{Level: 0.25})
		assertCode(t, resp, 400)
		resp.Body.Close()
	})

	t.Run("invalid volume level", func(t *testing.T) {
		resp := env.postJSON(t, "/api/volume", volumeRequest{Level: 1.5})
		assertCode(t, resp, 400)
		resp.Body.Close()
	})
}

func TestE2E_SwitchSpeakerWhilePlaying(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play on Bedroom
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.101"})
	s := decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after play: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Bedroom" {
		t.Fatalf("speaker_name: want Bedroom, got %s", s.SpeakerName)
	}
	if s.SpeakerIP != "192.168.1.101" {
		t.Fatalf("speaker_ip: want 192.168.1.101, got %s", s.SpeakerIP)
	}

	// Switch to Living Room by calling play with a different speaker
	resp = env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after switch: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("after switch speaker_name: want Living Room, got %s", s.SpeakerName)
	}
	if s.SpeakerIP != "192.168.1.100" {
		t.Fatalf("after switch speaker_ip: want 192.168.1.100, got %s", s.SpeakerIP)
	}

	// Status confirms the new speaker
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("status after switch: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("status speaker_name: want Living Room, got %s", s.SpeakerName)
	}
}

func TestE2E_SwitchSpeakerWhilePaused(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play on Bedroom
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.101"})
	resp.Body.Close()

	// Pause
	resp = env.postEmpty(t, "/api/pause")
	s := decodeStatus(t, resp)
	if s.State != cast.StatePaused {
		t.Fatalf("after pause: want paused, got %s", s.State)
	}
	if s.SpeakerName != "Bedroom" {
		t.Fatalf("paused speaker_name: want Bedroom, got %s", s.SpeakerName)
	}

	// Switch to Living Room (play on new speaker while paused on old one)
	resp = env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	s = decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after switch from paused: want playing, got %s", s.State)
	}
	if s.SpeakerName != "Living Room" {
		t.Fatalf("after switch speaker_name: want Living Room, got %s", s.SpeakerName)
	}
	if s.SpeakerIP != "192.168.1.100" {
		t.Fatalf("after switch speaker_ip: want 192.168.1.100, got %s", s.SpeakerIP)
	}
}

func TestE2E_AuthRequired(t *testing.T) {
	env := setupE2E(t, "admin", "secret")

	apiEndpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/status"},
		{"GET", "/api/speakers"},
		{"POST", "/api/play"},
		{"POST", "/api/pause"},
		{"POST", "/api/stop"},
		{"POST", "/api/volume"},
		{"POST", "/api/timer"},
		{"DELETE", "/api/timer"},
	}

	t.Run("API requires auth", func(t *testing.T) {
		for _, ep := range apiEndpoints {
			t.Run(ep.method+" "+ep.path, func(t *testing.T) {
				req, _ := http.NewRequest(ep.method, env.url(ep.path), nil)
				resp, err := env.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				assertCode(t, resp, 401)
			})
		}
	})

	t.Run("API works with creds", func(t *testing.T) {
		resp := env.getWithAuth(t, "/api/status", "admin", "secret")
		assertCode(t, resp, 200)
	})

	t.Run("static files need no auth", func(t *testing.T) {
		resp := env.get(t, "/")
		assertCode(t, resp, 200)

		resp = env.get(t, "/manifest.json")
		assertCode(t, resp, 200)
	})

	t.Run("audio needs no auth", func(t *testing.T) {
		resp := env.get(t, "/audio/test-secret/whitenoise.mp3")
		assertCode(t, resp, 200)
	})
}

func TestE2E_TimerSetAndCancel(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	s := decodeStatus(t, resp)
	if s.State != cast.StatePlaying {
		t.Fatalf("after play: want playing, got %s", s.State)
	}

	// Set timer
	resp = env.postJSON(t, "/api/timer", timerRequest{DurationS: 3600, Action: "stop"})
	assertCode(t, resp, 200)
	s = decodeStatus(t, resp)
	if !s.Timer.Active {
		t.Fatal("expected timer to be active after set")
	}
	if s.Timer.Action != cast.TimerActionStop {
		t.Errorf("timer action: want %q, got %q", cast.TimerActionStop, s.Timer.Action)
	}

	// Status confirms active timer
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if !s.Timer.Active {
		t.Fatal("expected timer to be active in status poll")
	}

	// Cancel timer
	resp = env.delete(t, "/api/timer")
	assertCode(t, resp, 200)
	s = decodeStatus(t, resp)
	if s.Timer.Active {
		t.Fatal("expected timer to be inactive after cancel")
	}

	// Status confirms timer gone
	resp = env.get(t, "/api/status")
	s = decodeStatus(t, resp)
	if s.Timer.Active {
		t.Fatal("expected timer to be inactive in status poll after cancel")
	}
}

func TestE2E_TimerAutoCancelOnStop(t *testing.T) {
	env := setupE2E(t, "", "")

	// Play
	resp := env.postJSON(t, "/api/play", playRequest{SpeakerIP: "192.168.1.100"})
	resp.Body.Close()

	// Set timer
	resp = env.postJSON(t, "/api/timer", timerRequest{DurationS: 3600, Action: "stop"})
	s := decodeStatus(t, resp)
	if !s.Timer.Active {
		t.Fatal("expected timer to be active")
	}

	// Stop playback — timer should auto-cancel
	resp = env.postEmpty(t, "/api/stop")
	s = decodeStatus(t, resp)
	if s.State != cast.StateDisconnected {
		t.Fatalf("after stop: want disconnected, got %s", s.State)
	}
	if s.Timer.Active {
		t.Fatal("expected timer to be cleared after stop")
	}
}

func TestE2E_TimerWhileDisconnected(t *testing.T) {
	env := setupE2E(t, "", "")

	// Set timer without playing — should fail
	resp := env.postJSON(t, "/api/timer", timerRequest{DurationS: 3600, Action: "stop"})
	assertCode(t, resp, 400)
}
