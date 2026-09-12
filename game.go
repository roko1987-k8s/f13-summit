package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reglas y estado del juego. No sabe nada de HTTP ni de SSE.

const (
	maxNameRunes = 20
	topSize      = 5

	statusLobby    = "lobby"
	statusRunning  = "running"
	statusFinished = "finished"
)

var (
	ErrNameExists  = errors.New("name_taken")
	ErrGameRunning = errors.New("game_running")
	ErrNotFound    = errors.New("player_not_found")
)

type Player struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Score    int    `json:"score"`
	JoinedAt int64  `json:"joinedAt"`
	Online   bool   `json:"online"`

	conns   int       // conexiones SSE abiertas para este jugador
	lastHit time.Time // último toque aceptado (rate limit)
}

type GameState struct {
	Status string `json:"status"` // lobby, running, finished
	// Epoch cambia en cada reset. El cliente lo compara con el que
	// recibió al unirse para saber que su sesión ya no existe.
	Epoch     int64 `json:"epoch"`
	StartedAt int64 `json:"startedAt"`
	EndsAt    int64 `json:"endsAt"`
	Duration  int64 `json:"duration"`
	// ServerNow permite al cliente corregir el desfase de su reloj.
	ServerNow int64 `json:"serverNow"`
}

// Snapshot es lo que se envía a los clientes. Players y Me solo se
// incluyen en /api/state, nunca en el stream SSE, para que el payload
// que se difunde a todos sea pequeño.
type Snapshot struct {
	Game        GameState `json:"game"`
	Top         []Player  `json:"top"`
	PlayerCount int       `json:"playerCount"`
	OnlineCount int       `json:"onlineCount"`
	// HitsPerSecond es el tráfico global de toques aceptados en los últimos
	// 2 s. El host lo usa para animar el autoscaling del cluster.
	HitsPerSecond float64  `json:"hitsPerSecond"`
	Players       []Player `json:"players,omitempty"`
	Me            *Player  `json:"me,omitempty"`
}

// Buckets de 100 ms para calcular toques por segundo sin guardar timestamps.
const (
	hitBucketMs = 100
	hitBuckets  = 20 // 2 s de ventana
)

type Game struct {
	mu       sync.RWMutex
	duration time.Duration
	minHit   time.Duration
	players  map[string]*Player
	state    GameState

	hitCount [hitBuckets]int
	hitAt    [hitBuckets]int64 // id del bucket (unixMs / 100) que ocupa cada posición

	// onChange se llama fuera del lock. immediate=true pide un broadcast
	// inmediato (start, reset, fin de partida); false lo deja para el tick.
	onChange func(immediate bool)
}

func NewGame(duration, minHit time.Duration) *Game {
	return &Game{
		duration: duration,
		minHit:   minHit,
		players:  make(map[string]*Player),
		state: GameState{
			Status: statusLobby,
			Epoch:  time.Now().UnixMilli(),
		},
		onChange: func(bool) {},
	}
}

var randomNames = []string{
	"PixelGopher", "KubeNinja", "GoRunner", "PodMaster", "CloudFox",
	"ByteBear", "KindHero", "GopherAce", "NodeWizard", "ClusterKid",
	"GoPilot", "TinyPod", "KubeRider", "PixelBot", "CloudKid",
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func randomName() string {
	return fmt.Sprintf(
		"%s-%d",
		randomNames[mathrand.IntN(len(randomNames))],
		mathrand.IntN(90)+10,
	)
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// truncateName corta por runas, no por bytes, para no partir
// caracteres como "ñ" por la mitad.
func truncateName(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) > maxNameRunes {
		name = strings.TrimSpace(string(runes[:maxNameRunes]))
	}
	return name
}

// Requiere lock.
func (g *Game) nameExists(name string) bool {
	normalized := normalizeName(name)
	for _, p := range g.players {
		if normalizeName(p.Name) == normalized {
			return true
		}
	}
	return false
}

// Requiere lock.
func (g *Game) freeRandomName() string {
	for i := 0; i < 50; i++ {
		if candidate := randomName(); !g.nameExists(candidate) {
			return candidate
		}
	}
	// Muy improbable (15 × 90 combinaciones), pero nunca bloqueamos.
	return "Gopher-" + newID()[:4]
}

// ----------------------------------------------------
// JOIN
// ----------------------------------------------------

func (g *Game) Join(name string) (Player, error) {
	name = truncateName(name)

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.state.Status == statusRunning && time.Now().UnixMilli() < g.state.EndsAt {
		return Player{}, ErrGameRunning
	}

	if name == "" {
		name = g.freeRandomName()
	} else if g.nameExists(name) {
		return Player{}, ErrNameExists
	}

	// Online lo activa la conexión SSE (Connect), no el join: así un
	// cliente que solo llama a la API (bots, k6) no cuenta como conectado.
	p := &Player{
		ID:       newID(),
		Name:     name,
		JoinedAt: time.Now().UnixMilli(),
	}
	g.players[p.ID] = p

	go g.onChange(false)

	return *p, nil
}

// ----------------------------------------------------
// HIT
// ----------------------------------------------------

// Hit suma un punto si la partida está en curso y el jugador no está
// tocando más rápido que minHit. Devuelve el score actual en cualquier caso.
func (g *Game) Hit(id string) (score int, accepted bool, err error) {
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[id]
	if !ok {
		return 0, false, ErrNotFound
	}

	running := g.state.Status == statusRunning && now.UnixMilli() < g.state.EndsAt
	if !running {
		return p.Score, false, nil
	}

	if now.Sub(p.lastHit) < g.minHit {
		return p.Score, false, nil
	}

	p.lastHit = now
	p.Score++
	g.countHit(now)

	go g.onChange(false)

	return p.Score, true, nil
}

// Requiere lock.
func (g *Game) countHit(now time.Time) {
	bucket := now.UnixMilli() / hitBucketMs
	i := bucket % hitBuckets

	if g.hitAt[i] != bucket {
		g.hitAt[i] = bucket
		g.hitCount[i] = 0
	}
	g.hitCount[i]++
}

// Requiere lock (de lectura). Toques aceptados por segundo en la ventana.
func (g *Game) hitsPerSecond(now time.Time) float64 {
	bucket := now.UnixMilli() / hitBucketMs
	total := 0

	for i := range g.hitCount {
		if bucket-g.hitAt[i] < hitBuckets {
			total += g.hitCount[i]
		}
	}

	return float64(total) / (float64(hitBuckets*hitBucketMs) / 1000)
}

// ----------------------------------------------------
// START / FINISH / RESET
// ----------------------------------------------------

func (g *Game) Start() GameState {
	now := time.Now()
	end := now.Add(g.duration)

	g.mu.Lock()

	for _, p := range g.players {
		p.Score = 0
		p.lastHit = time.Time{}
	}

	g.hitCount = [hitBuckets]int{}
	g.hitAt = [hitBuckets]int64{}

	g.state.Status = statusRunning
	g.state.StartedAt = now.UnixMilli()
	g.state.EndsAt = end.UnixMilli()
	g.state.Duration = int64(g.duration.Seconds())

	state := g.state

	g.mu.Unlock()

	time.AfterFunc(time.Until(end), func() { g.finish(end.UnixMilli()) })

	g.onChange(true)

	return state
}

// finish cierra la partida solo si sigue siendo la misma que se programó
// (expectedEnd), para no cerrar una ronda nueva por accidente.
func (g *Game) finish(expectedEnd int64) {
	g.mu.Lock()

	if g.state.Status != statusRunning || g.state.EndsAt != expectedEnd {
		g.mu.Unlock()
		return
	}

	g.state.Status = statusFinished

	g.mu.Unlock()

	g.onChange(true)
}

func (g *Game) Reset() GameState {
	g.mu.Lock()

	g.state = GameState{
		Status: statusLobby,
		Epoch:  time.Now().UnixMilli(),
	}
	g.players = make(map[string]*Player)

	state := g.state

	g.mu.Unlock()

	g.onChange(true)

	return state
}

// ----------------------------------------------------
// PRESENCIA
// ----------------------------------------------------

// Connect registra una conexión SSE del jugador. Devuelve false si no existe.
func (g *Game) Connect(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[id]
	if !ok {
		return false
	}

	p.conns++
	if !p.Online {
		p.Online = true
		go g.onChange(false)
	}
	return true
}

func (g *Game) Disconnect(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[id]
	if !ok {
		return
	}

	p.conns--
	if p.conns <= 0 {
		p.conns = 0
		p.Online = false
		go g.onChange(false)
	}
}

// ----------------------------------------------------
// SNAPSHOT
// ----------------------------------------------------

// Snapshot devuelve el estado para los clientes. Con full=true incluye la
// lista completa de jugadores; meID añade el jugador indicado en Me.
func (g *Game) Snapshot(full bool, meID string) Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	players := make([]Player, 0, len(g.players))
	online := 0

	for _, p := range g.players {
		players = append(players, *p)
		if p.Online {
			online++
		}
	}

	sort.Slice(players, func(i, j int) bool {
		if players[i].Score == players[j].Score {
			return players[i].JoinedAt < players[j].JoinedAt
		}
		return players[i].Score > players[j].Score
	})

	top := players
	if len(top) > topSize {
		top = top[:topSize]
	}

	now := time.Now()

	snap := Snapshot{
		Game:          g.state,
		Top:           top,
		PlayerCount:   len(players),
		OnlineCount:   online,
		HitsPerSecond: g.hitsPerSecond(now),
	}
	snap.Game.ServerNow = now.UnixMilli()

	if full {
		snap.Players = players
	}

	if meID != "" {
		if p, ok := g.players[meID]; ok {
			me := *p
			snap.Me = &me
		}
	}

	return snap
}

// Active indica si conviene seguir emitiendo snapshots aunque no haya
// cambios: durante la partida y unos segundos después, hasta que la
// ventana de req/s se vacía y los clientes ven el tráfico en cero.
func (g *Game) Active(now time.Time) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	const drain = int64(hitBuckets*hitBucketMs) + 1000

	switch g.state.Status {
	case statusRunning:
		return true
	case statusFinished:
		return now.UnixMilli()-g.state.EndsAt < drain
	}
	return false
}

// Epoch devuelve el identificador de la sesión actual (cambia con cada reset).
func (g *Game) Epoch() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state.Epoch
}
