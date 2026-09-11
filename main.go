package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mathrand "math/rand/v2"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"
)

const gameDuration = 30 * time.Second

type Player struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Score    int    `json:"score"`
	JoinedAt int64  `json:"joinedAt"`
	Online   bool   `json:"online"`
}

type GameState struct {
	Status    string `json:"status"` // lobby, running, finished
	StartedAt int64  `json:"startedAt"`
	EndsAt    int64  `json:"endsAt"`
	Duration  int64  `json:"duration"`
}

type Server struct {
	mu      sync.RWMutex
	players map[string]*Player
	clients map[chan []byte]struct{}
	game    GameState
}

var randomNames = []string{
	"PixelGopher",
	"KubeNinja",
	"GoRunner",
	"PodMaster",
	"CloudFox",
	"ByteBear",
	"KindHero",
	"GopherAce",
	"NodeWizard",
	"ClusterKid",
	"GoPilot",
	"TinyPod",
	"KubeRider",
	"PixelBot",
	"CloudKid",
}

// ----------------------------------------------------
// ID ALEATORIO
// ----------------------------------------------------

func newID() string {
	b := make([]byte, 6)

	if _, err := rand.Read(b); err != nil {
		panic(err)
	}

	return hex.EncodeToString(b)
}

// ----------------------------------------------------
// NOMBRE ALEATORIO
// ----------------------------------------------------

func randomName() string {
	return fmt.Sprintf(
		"%s-%d",
		randomNames[mathrand.IntN(len(randomNames))],
		mathrand.IntN(99)+1,
	)
}

// ----------------------------------------------------
// SNAPSHOT
// ----------------------------------------------------

func (s *Server) snapshot() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	players := make([]Player, 0, len(s.players))

	for _, player := range s.players {
		players = append(players, *player)
	}

	// Ordenar por score.
	sort.Slice(players, func(i, j int) bool {
		if players[i].Score == players[j].Score {
			return players[i].JoinedAt < players[j].JoinedAt
		}

		return players[i].Score > players[j].Score
	})

	// TOP 5
	top := players

	if len(top) > 5 {
		top = top[:5]
	}

	response := struct {
		Game    GameState `json:"game"`
		Players []Player  `json:"players"`
		Top     []Player  `json:"top"`
	}{
		Game:    s.game,
		Players: players,
		Top:     top,
	}

	data, err := json.Marshal(response)

	if err != nil {
		return []byte(`{"error":"could not serialize state"}`)
	}

	return data
}

// ----------------------------------------------------
// BROADCAST
// ----------------------------------------------------

func (s *Server) broadcast() {
	message := s.snapshot()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for client := range s.clients {

		select {
		case client <- message:
		default:
			// No bloqueamos al resto de jugadores
			// si un cliente está lento.
		}
	}
}

// ----------------------------------------------------
// JOIN
// ----------------------------------------------------

func (s *Server) join(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var request struct {
		Name string `json:"name"`
	}

	_ = json.NewDecoder(r.Body).Decode(&request)

	name := request.Name

	// Si no colocó nombre → aleatorio.
	if name == "" {
		name = randomName()
	}

	// Limitar nombre.
	if len(name) > 20 {
		name = name[:20]
	}

	player := &Player{
		ID:       newID(),
		Name:     name,
		Score:    0,
		JoinedAt: time.Now().UnixMilli(),
		Online:   true,
	}

	s.mu.Lock()

	s.players[player.ID] = player

	s.mu.Unlock()

	s.broadcast()

	writeJSON(w, player)
}

// ----------------------------------------------------
// SCORE
// ----------------------------------------------------

func (s *Server) score(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	playerID := r.URL.Query().Get("id")

	s.mu.Lock()

	player, exists := s.players[playerID]

	now := time.Now()

	gameRunning :=
		s.game.Status == "running" &&
		now.UnixMilli() < s.game.EndsAt

	if !exists {
		s.mu.Unlock()

		http.Error(
			w,
			"player not found",
			http.StatusNotFound,
		)

		return
	}

	if gameRunning {
		// Cada toque = +1 punto
		player.Score++
	}

	score := player.Score

	s.mu.Unlock()

	if gameRunning {
		s.broadcast()
	}

	writeJSON(w, map[string]any{
		"score":    score,
		"accepted": gameRunning,
	})
}

// ----------------------------------------------------
// START GAME
// ----------------------------------------------------

func (s *Server) start(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	s.mu.Lock()

	now := time.Now()
	end := now.Add(gameDuration)

	// Reiniciar scores.
	for _, player := range s.players {
		player.Score = 0
	}

	s.game = GameState{
		Status:    "running",
		StartedAt: now.UnixMilli(),
		EndsAt:    end.UnixMilli(),
		Duration:  int64(gameDuration.Seconds()),
	}

	s.mu.Unlock()

	// Avisar a todos inmediatamente.
	s.broadcast()

	// Esperar hasta que termine.
	go func(expectedEnd int64) {

		timer := time.NewTimer(
			time.Until(end),
		)

		defer timer.Stop()

		<-timer.C

		s.mu.Lock()

		// Evitar cerrar una partida nueva accidentalmente.
		if s.game.Status == "running" &&
			s.game.EndsAt == expectedEnd {

			s.game.Status = "finished"
		}

		s.mu.Unlock()

		s.broadcast()

	}(end.UnixMilli())

	writeJSON(w, map[string]any{
		"status":   "running",
		"duration": int(gameDuration.Seconds()),
	})
}

// ----------------------------------------------------
// RESET
// ----------------------------------------------------

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	s.mu.Lock()

	s.game = GameState{
		Status: "lobby",
	}

	s.players = make(map[string]*Player)

	s.mu.Unlock()

	s.broadcast()

	writeJSON(w, map[string]string{
		"status": "lobby",
	})
}

// ----------------------------------------------------
// STATE
// ----------------------------------------------------

func (s *Server) state(w http.ResponseWriter, r *http.Request) {

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.Write(s.snapshot())
}

// ----------------------------------------------------
// SERVER-SENT EVENTS
// ----------------------------------------------------

func (s *Server) events(w http.ResponseWriter, r *http.Request) {

	flusher, ok := w.(http.Flusher)

	if !ok {
		http.Error(
			w,
			"SSE not supported",
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"text/event-stream",
	)

	w.Header().Set(
		"Cache-Control",
		"no-cache",
	)

	w.Header().Set(
		"Connection",
		"keep-alive",
	)

	client := make(chan []byte, 8)

	s.mu.Lock()

	s.clients[client] = struct{}{}

	s.mu.Unlock()

	defer func() {

		s.mu.Lock()

		delete(s.clients, client)

		s.mu.Unlock()

		close(client)

	}()

	// Estado inicial.
	fmt.Fprintf(
		w,
		"data: %s\n\n",
		s.snapshot(),
	)

	flusher.Flush()

	for {

		select {

		case message := <-client:

			fmt.Fprintf(
				w,
				"data: %s\n\n",
				message,
			)

			flusher.Flush()

		case <-r.Context().Done():

			return
		}
	}
}

// ----------------------------------------------------
// JSON
// ----------------------------------------------------

func writeJSON(w http.ResponseWriter, value any) {

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	_ = json.NewEncoder(w).Encode(value)
}

// ----------------------------------------------------
// MAIN
// ----------------------------------------------------

func main() {

	server := &Server{
		players: make(map[string]*Player),
		clients: make(map[chan []byte]struct{}),
		game: GameState{
			Status: "lobby",
		},
	}

	mux := http.NewServeMux()

	// Frontend
	mux.Handle(
		"/",
		http.FileServer(
			http.Dir("./static"),
		),
	)

	// API
	mux.HandleFunc(
		"/api/join",
		server.join,
	)

	mux.HandleFunc(
		"/api/score",
		server.score,
	)

	mux.HandleFunc(
		"/api/start",
		server.start,
	)

	mux.HandleFunc(
		"/api/reset",
		server.reset,
	)

	mux.HandleFunc(
		"/api/state",
		server.state,
	)

	// Tiempo real
	mux.HandleFunc(
		"/api/events",
		server.events,
	)

	addr := ":443"

	log.Printf(
		"🎮 F13 Kind Game running on %s",
		addr,
	)

	log.Printf(
		"⏱️ Game duration: %s",
		gameDuration,
	)

	log.Fatal(
		http.ListenAndServe(addr, mux),
	)
}
