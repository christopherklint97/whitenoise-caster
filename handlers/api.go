package handlers

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/telnesstech/whitenoise-caster/cast"
	"github.com/telnesstech/whitenoise-caster/config"
)

// Caster controls Chromecast playback.
type Caster interface {
	Play(ctx context.Context, speakerIP, speakerName string) error
	Pause() error
	Stop() error
	GetStatus() cast.Status
}

type Handler struct {
	cfg        *config.Config
	controller Caster
	log        *slog.Logger
	webFS      fs.FS
}

func New(cfg *config.Config, controller Caster, logger *slog.Logger, webFS fs.FS) *Handler {
	return &Handler{
		cfg:        cfg,
		controller: controller,
		log:        logger,
		webFS:      webFS,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static / embedded files
	mux.HandleFunc("GET /", h.serveIndex)
	mux.HandleFunc("GET /manifest.json", h.serveFile("manifest.json", "application/manifest+json"))
	mux.HandleFunc("GET /icon.png", h.serveFile("icon.png", "image/png"))

	// Audio endpoint (no auth — Chromecast must reach this)
	mux.HandleFunc("GET /audio/{secret}/whitenoise.mp3", h.serveAudio)

	// API endpoints (with optional auth)
	mux.HandleFunc("POST /api/play", h.withAuth(h.handlePlay))
	mux.HandleFunc("POST /api/pause", h.withAuth(h.handlePause))
	mux.HandleFunc("POST /api/stop", h.withAuth(h.handleStop))
	mux.HandleFunc("GET /api/status", h.withAuth(h.handleStatus))
	mux.HandleFunc("GET /api/speakers", h.withAuth(h.handleSpeakers))
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	if !h.cfg.HasAuth() {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != h.cfg.Auth.Username || pass != h.cfg.Auth.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="whitenoise"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(h.webFS, "index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (h *Handler) serveFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(h.webFS, name)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

func (h *Handler) serveAudio(w http.ResponseWriter, r *http.Request) {
	secret := r.PathValue("secret")
	if secret != h.cfg.SecretPath {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(h.cfg.AudioFile)
	if err != nil {
		h.log.Error("opening audio file", "error", err)
		http.Error(w, "audio not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, "whitenoise.mp3", stat.ModTime(), f)
}

type playRequest struct {
	SpeakerIP string `json:"speaker_ip"`
}

func (h *Handler) handlePlay(w http.ResponseWriter, r *http.Request) {
	var req playRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	speaker := h.cfg.SpeakerByIP(req.SpeakerIP)
	if speaker == nil {
		jsonError(w, "unknown speaker", http.StatusBadRequest)
		return
	}

	if err := h.controller.Play(r.Context(), speaker.IP, speaker.Name); err != nil {
		h.log.Error("play failed", "error", err)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, h.controller.GetStatus())
}

func (h *Handler) handlePause(w http.ResponseWriter, r *http.Request) {
	if err := h.controller.Pause(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, h.controller.GetStatus())
}

func (h *Handler) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := h.controller.Stop(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, h.controller.GetStatus())
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, h.controller.GetStatus())
}

func (h *Handler) handleSpeakers(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, h.cfg.Speakers)
}

func jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
