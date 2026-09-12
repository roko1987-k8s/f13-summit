package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const ssePingInterval = 25 * time.Second

type Server struct {
	cfg  Config
	game *Game
	hub  *Hub
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Frontend
	mux.Handle("/", cacheControl(http.FileServer(http.Dir(s.cfg.StaticDir))))

	// API
	mux.HandleFunc("POST /api/join", s.join)
	mux.HandleFunc("POST /api/score", s.score)
	mux.HandleFunc("POST /api/start", s.requireHost(s.start))
	mux.HandleFunc("POST /api/reset", s.requireHost(s.reset))
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/qr.png", s.qr)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	// Cualquier otra ruta /api/ (incluido un método equivocado) responde
	// como API y no cae en el FileServer.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found")
	})

	return mux
}

// ----------------------------------------------------
// CACHÉ DE ESTÁTICOS
// ----------------------------------------------------

// cacheControl evita que los teléfonos se queden con un index.html, CSS o
// JS antiguo tras un despliegue: esos archivos se revalidan siempre (el
// FileServer responde 304 si no cambiaron). Imágenes y fuentes no cambian
// entre despliegues, así que se cachean un día.
func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// ----------------------------------------------------
// AUTH DEL HOST
// ----------------------------------------------------

// requireHost exige la cabecera X-Host-Token cuando HOST_TOKEN está
// configurado. Sin token configurado (desarrollo) deja pasar a todos.
func (s *Server) requireHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.HostToken != "" {
			got := r.Header.Get("X-Host-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.HostToken)) != 1 {
				writeError(w, http.StatusForbidden, "invalid_host_token")
				return
			}
		}
		next(w, r)
	}
}

// ----------------------------------------------------
// JOIN
// ----------------------------------------------------

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&request)

	player, err := s.game.Join(request.Name)

	switch {
	case errors.Is(err, ErrNameExists), errors.Is(err, ErrGameRunning):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "join_failed")
		return
	}

	writeJSON(w, map[string]any{
		"id":       player.ID,
		"name":     player.Name,
		"score":    player.Score,
		"joinedAt": player.JoinedAt,
		"epoch":    s.game.Epoch(),
	})
}

// ----------------------------------------------------
// SCORE
// ----------------------------------------------------

func (s *Server) score(w http.ResponseWriter, r *http.Request) {
	score, accepted, err := s.game.Hit(r.URL.Query().Get("id"))

	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"score":    score,
		"accepted": accepted,
	})
}

// ----------------------------------------------------
// START / RESET
// ----------------------------------------------------

func (s *Server) start(w http.ResponseWriter, _ *http.Request) {
	state := s.game.Start()

	writeJSON(w, map[string]any{
		"status":   state.Status,
		"duration": state.Duration,
	})
}

func (s *Server) reset(w http.ResponseWriter, _ *http.Request) {
	state := s.game.Reset()

	writeJSON(w, map[string]any{
		"status": state.Status,
	})
}

// ----------------------------------------------------
// STATE
// ----------------------------------------------------

// state devuelve el snapshot completo. Con ?id= incluye al jugador en "me",
// que el cliente usa para recuperar su sesión tras recargar.
func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.game.Snapshot(true, r.URL.Query().Get("id")))
}

// ----------------------------------------------------
// SERVER-SENT EVENTS
// ----------------------------------------------------

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Presencia: si el stream trae ?id=, el jugador cuenta como online
	// mientras la conexión siga abierta.
	if id := r.URL.Query().Get("id"); id != "" && s.game.Connect(id) {
		defer s.game.Disconnect(id)
	}

	client := s.hub.Subscribe()
	defer s.hub.Unsubscribe(client)

	// Estado inicial.
	fmt.Fprintf(w, "data: %s\n\n", s.hub.payload())
	flusher.Flush()

	// Los proxies cierran conexiones inactivas; el ping las mantiene vivas.
	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()

	for {
		select {
		case message := <-client:
			fmt.Fprintf(w, "data: %s\n\n", message)
			flusher.Flush()

		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

// ----------------------------------------------------
// QR
// ----------------------------------------------------

// qr genera el código QR de la URL de jugadores para la pantalla del host.
// El cliente manda su propio origin porque el servidor no conoce la URL
// pública detrás del ingress.
func (s *Server) qr(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")

	u, err := url.Parse(raw)
	if err != nil || len(raw) > 256 || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "invalid_url")
		return
	}

	png, err := qrcode.Encode(raw, qrcode.Medium, 512)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed")
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

// ----------------------------------------------------
// JSON
// ----------------------------------------------------

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": strings.TrimSpace(code)})
}
