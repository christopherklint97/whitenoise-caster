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
	"testing"
	"testing/fstest"

	"github.com/telnesstech/whitenoise-caster/cast"
	"github.com/telnesstech/whitenoise-caster/config"
)

// mockCaster implements Caster for testing.
type mockCaster struct {
	playFunc      func(ctx context.Context, ip, name string) error
	pauseFunc     func() error
	stopFunc      func() error
	setVolumeFunc func(level float32) error
	status        cast.Status
}

func (m *mockCaster) Play(ctx context.Context, ip, name string) error {
	if m.playFunc != nil {
		return m.playFunc(ctx, ip, name)
	}
	return nil
}

func (m *mockCaster) Pause() error {
	if m.pauseFunc != nil {
		return m.pauseFunc()
	}
	return nil
}

func (m *mockCaster) Stop() error {
	if m.stopFunc != nil {
		return m.stopFunc()
	}
	return nil
}

func (m *mockCaster) SetVolume(level float32) error {
	if m.setVolumeFunc != nil {
		return m.setVolumeFunc(level)
	}
	return nil
}

func (m *mockCaster) GetStatus() cast.Status {
	return m.status
}

func testConfig() *config.Config {
	return &config.Config{
		Speakers: []config.Speaker{
			{Name: "Living Room", IP: "192.168.1.100"},
			{Name: "Bedroom", IP: "192.168.1.101"},
		},
		AudioFile:  "/tmp/test.mp3",
		AudioURL:   "https://example.com",
		ListenAddr: ":8080",
		SecretPath: "test-secret",
	}
}

func setupHandler(t *testing.T, mock *mockCaster, cfg *config.Config) *http.ServeMux {
	t.Helper()
	if cfg == nil {
		cfg = testConfig()
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	h := New(cfg, mock, logger, webFS)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func TestHandleStatus(t *testing.T) {
	mock := &mockCaster{
		status: cast.Status{
			State:       cast.StatePlaying,
			SpeakerIP:   "192.168.1.100",
			SpeakerName: "Living Room",
		},
	}
	mux := setupHandler(t, mock, nil)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got cast.Status
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != cast.StatePlaying {
		t.Errorf("state: want %q, got %q", cast.StatePlaying, got.State)
	}
	if got.SpeakerName != "Living Room" {
		t.Errorf("speaker_name: want %q, got %q", "Living Room", got.SpeakerName)
	}
	if got.SpeakerIP != "192.168.1.100" {
		t.Errorf("speaker_ip: want %q, got %q", "192.168.1.100", got.SpeakerIP)
	}
}

func TestHandleSpeakers(t *testing.T) {
	mock := &mockCaster{}
	mux := setupHandler(t, mock, nil)

	req := httptest.NewRequest("GET", "/api/speakers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var speakers []config.Speaker
	if err := json.NewDecoder(w.Body).Decode(&speakers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(speakers) != 2 {
		t.Fatalf("want 2 speakers, got %d", len(speakers))
	}
	if speakers[0].Name != "Living Room" {
		t.Errorf("speakers[0].Name: want %q, got %q", "Living Room", speakers[0].Name)
	}
	if speakers[1].IP != "192.168.1.101" {
		t.Errorf("speakers[1].IP: want %q, got %q", "192.168.1.101", speakers[1].IP)
	}
}

func TestHandlePlay(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotIP, gotName string
		mock := &mockCaster{
			playFunc: func(_ context.Context, ip, name string) error {
				gotIP = ip
				gotName = name
				return nil
			},
			status: cast.Status{
				State:       cast.StatePlaying,
				SpeakerIP:   "192.168.1.100",
				SpeakerName: "Living Room",
			},
		}
		mux := setupHandler(t, mock, nil)

		body := `{"speaker_ip":"192.168.1.100"}`
		req := httptest.NewRequest("POST", "/api/play", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		if gotIP != "192.168.1.100" {
			t.Errorf("Play IP: want %q, got %q", "192.168.1.100", gotIP)
		}
		if gotName != "Living Room" {
			t.Errorf("Play Name: want %q, got %q", "Living Room", gotName)
		}

		var got cast.Status
		json.NewDecoder(w.Body).Decode(&got)
		if got.State != cast.StatePlaying {
			t.Errorf("response state: want %q, got %q", cast.StatePlaying, got.State)
		}
	})

	t.Run("unknown speaker", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		body := `{"speaker_ip":"10.0.0.1"}`
		req := httptest.NewRequest("POST", "/api/play", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["error"] != "unknown speaker" {
			t.Errorf("error: want %q, got %q", "unknown speaker", resp["error"])
		}
	})

	t.Run("empty body", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/play", bytes.NewBufferString(""))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/play", bytes.NewBufferString("{not json"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("missing speaker_ip", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		body := `{"speaker_ip":""}`
		req := httptest.NewRequest("POST", "/api/play", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("play error", func(t *testing.T) {
		mock := &mockCaster{
			playFunc: func(_ context.Context, ip, name string) error {
				return fmt.Errorf("connection refused")
			},
			status: cast.Status{State: cast.StateError, Error: "connection refused"},
		}
		mux := setupHandler(t, mock, nil)

		body := `{"speaker_ip":"192.168.1.100"}`
		req := httptest.NewRequest("POST", "/api/play", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["error"] != "connection refused" {
			t.Errorf("error: want %q, got %q", "connection refused", resp["error"])
		}
	})
}

func TestHandlePause(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pauseCalled := false
		mock := &mockCaster{
			pauseFunc: func() error {
				pauseCalled = true
				return nil
			},
			status: cast.Status{State: cast.StatePaused, SpeakerName: "Living Room"},
		}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/pause", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		if !pauseCalled {
			t.Error("expected Pause() to be called")
		}

		var got cast.Status
		json.NewDecoder(w.Body).Decode(&got)
		if got.State != cast.StatePaused {
			t.Errorf("state: want %q, got %q", cast.StatePaused, got.State)
		}
	})

	t.Run("not connected error", func(t *testing.T) {
		mock := &mockCaster{
			pauseFunc: func() error {
				return fmt.Errorf("not connected")
			},
		}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/pause", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestHandleStop(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		stopCalled := false
		mock := &mockCaster{
			stopFunc: func() error {
				stopCalled = true
				return nil
			},
			status: cast.Status{State: cast.StateDisconnected},
		}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/stop", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		if !stopCalled {
			t.Error("expected Stop() to be called")
		}

		var got cast.Status
		json.NewDecoder(w.Body).Decode(&got)
		if got.State != cast.StateDisconnected {
			t.Errorf("state: want %q, got %q", cast.StateDisconnected, got.State)
		}
	})

	t.Run("error", func(t *testing.T) {
		mock := &mockCaster{
			stopFunc: func() error {
				return fmt.Errorf("stop failed")
			},
		}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/stop", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("already disconnected is ok", func(t *testing.T) {
		mock := &mockCaster{
			status: cast.Status{State: cast.StateDisconnected},
		}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/stop", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestHandleVolume(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotLevel float32
		mock := &mockCaster{
			setVolumeFunc: func(level float32) error {
				gotLevel = level
				return nil
			},
			status: cast.Status{State: cast.StatePlaying, SpeakerName: "Living Room"},
		}
		mux := setupHandler(t, mock, nil)

		body := `{"level":0.3}`
		req := httptest.NewRequest("POST", "/api/volume", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		if gotLevel != 0.3 {
			t.Errorf("level: want 0.3, got %f", gotLevel)
		}
	})

	t.Run("level too high", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		body := `{"level":1.5}`
		req := httptest.NewRequest("POST", "/api/volume", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("level negative", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		body := `{"level":-0.1}`
		req := httptest.NewRequest("POST", "/api/volume", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid body", func(t *testing.T) {
		mock := &mockCaster{}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("POST", "/api/volume", bytes.NewBufferString("{bad"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("not connected error", func(t *testing.T) {
		mock := &mockCaster{
			setVolumeFunc: func(level float32) error {
				return fmt.Errorf("not connected")
			},
		}
		mux := setupHandler(t, mock, nil)

		body := `{"level":0.2}`
		req := httptest.NewRequest("POST", "/api/volume", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestAuth(t *testing.T) {
	t.Run("rejects unauthenticated when configured", func(t *testing.T) {
		mock := &mockCaster{status: cast.Status{State: cast.StateDisconnected}}
		cfg := testConfig()
		cfg.Auth.Username = "admin"
		cfg.Auth.Password = "secret"
		mux := setupHandler(t, mock, cfg)

		req := httptest.NewRequest("GET", "/api/status", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 without auth, got %d", w.Code)
		}
		if w.Header().Get("WWW-Authenticate") == "" {
			t.Error("expected WWW-Authenticate header")
		}
	})

	t.Run("rejects wrong credentials", func(t *testing.T) {
		mock := &mockCaster{status: cast.Status{State: cast.StateDisconnected}}
		cfg := testConfig()
		cfg.Auth.Username = "admin"
		cfg.Auth.Password = "secret"
		mux := setupHandler(t, mock, cfg)

		req := httptest.NewRequest("GET", "/api/status", nil)
		req.SetBasicAuth("admin", "wrong")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 with wrong password, got %d", w.Code)
		}
	})

	t.Run("accepts correct credentials", func(t *testing.T) {
		mock := &mockCaster{status: cast.Status{State: cast.StateDisconnected}}
		cfg := testConfig()
		cfg.Auth.Username = "admin"
		cfg.Auth.Password = "secret"
		mux := setupHandler(t, mock, cfg)

		req := httptest.NewRequest("GET", "/api/status", nil)
		req.SetBasicAuth("admin", "secret")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 with correct auth, got %d", w.Code)
		}
	})

	t.Run("no auth required when not configured", func(t *testing.T) {
		mock := &mockCaster{status: cast.Status{State: cast.StateDisconnected}}
		mux := setupHandler(t, mock, nil)

		req := httptest.NewRequest("GET", "/api/status", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 without auth config, got %d", w.Code)
		}
	})

	t.Run("auth applies to all API endpoints", func(t *testing.T) {
		mock := &mockCaster{status: cast.Status{State: cast.StateDisconnected}}
		cfg := testConfig()
		cfg.Auth.Username = "admin"
		cfg.Auth.Password = "secret"
		mux := setupHandler(t, mock, cfg)

		endpoints := []struct {
			method string
			path   string
		}{
			{"GET", "/api/status"},
			{"GET", "/api/speakers"},
			{"POST", "/api/play"},
			{"POST", "/api/pause"},
			{"POST", "/api/stop"},
			{"POST", "/api/volume"},
		}

		for _, ep := range endpoints {
			t.Run(ep.method+" "+ep.path, func(t *testing.T) {
				req := httptest.NewRequest(ep.method, ep.path, nil)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)

				if w.Code != http.StatusUnauthorized {
					t.Errorf("expected 401, got %d", w.Code)
				}
			})
		}
	})
}

func TestContentType(t *testing.T) {
	mock := &mockCaster{status: cast.Status{State: cast.StateDisconnected}}
	mux := setupHandler(t, mock, nil)

	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/status", ""},
		{"GET", "/api/speakers", ""},
		{"POST", "/api/stop", ""},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var bodyReader io.Reader
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			}
			req := httptest.NewRequest(ep.method, ep.path, bodyReader)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type: want %q, got %q", "application/json", ct)
			}
		})
	}
}

func TestWrongMethodOnPostEndpoints(t *testing.T) {
	mock := &mockCaster{}
	mux := setupHandler(t, mock, nil)

	// GET on a POST-only endpoint — the catch-all GET / matches first,
	// and serveIndex rejects non-root paths with 404.
	req := httptest.NewRequest("GET", "/api/play", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	// POST on a GET-only endpoint should be 405
	req = httptest.NewRequest("POST", "/api/status", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
