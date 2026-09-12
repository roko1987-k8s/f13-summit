package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Hub reparte el estado a las conexiones SSE.
//
// En vez de emitir por cada punto (con 500 jugadores tocando serían miles de
// snapshots por segundo), los cambios marcan el hub como "dirty" y un ticker
// emite como máximo una vez por intervalo. Los eventos importantes
// (start, reset, fin) usan Broadcast() y salen de inmediato.
type Hub struct {
	mu       sync.Mutex
	clients  map[chan []byte]struct{}
	dirty    atomic.Bool
	interval time.Duration
	payload  func() []byte

	// Heartbeat, si está definido y devuelve true, fuerza un broadcast
	// cada HeartbeatEvery aunque no haya cambios. Sirve para que las
	// métricas derivadas del tiempo (req/s) lleguen a cero en los clientes
	// cuando el tráfico para: sin cambios no habría snapshots nuevos y el
	// host se quedaría con el último valor alto.
	Heartbeat      func() bool
	HeartbeatEvery time.Duration
}

func NewHub(interval time.Duration, payload func() []byte) *Hub {
	return &Hub{
		clients:        make(map[chan []byte]struct{}),
		interval:       interval,
		payload:        payload,
		HeartbeatEvery: time.Second,
	}
}

func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, 8)

	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	return ch
}

func (h *Hub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// MarkDirty pide un broadcast en el próximo tick.
func (h *Hub) MarkDirty() {
	h.dirty.Store(true)
}

// Broadcast emite el estado actual a todos los clientes ahora mismo.
func (h *Hub) Broadcast() {
	h.dirty.Store(false)

	message := h.payload()

	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		select {
		case client <- message:
		default:
			// Un cliente lento no bloquea al resto; recibirá el siguiente.
		}
	}
}

// Run emite mientras haya cambios pendientes, una vez por intervalo, y un
// latido periódico mientras Heartbeat lo pida.
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	lastBeat := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			beat := h.Heartbeat != nil && now.Sub(lastBeat) >= h.HeartbeatEvery && h.Heartbeat()

			if h.dirty.Swap(false) || beat {
				lastBeat = now
				h.Broadcast()
			}
		}
	}
}
