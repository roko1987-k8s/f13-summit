package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type Config struct {
	Port              string
	StaticDir         string
	GameDuration      time.Duration
	HostToken         string
	BroadcastInterval time.Duration
	MinHitInterval    time.Duration
}

func loadConfig() Config {
	cfg := Config{
		Port:              envOr("PORT", "8080"),
		StaticDir:         envOr("STATIC_DIR", "./static"),
		GameDuration:      envSeconds("GAME_DURATION", 30),
		HostToken:         os.Getenv("HOST_TOKEN"),
		BroadcastInterval: envMillis("BROADCAST_INTERVAL_MS", 150),
		MinHitInterval:    envMillis("MIN_HIT_INTERVAL_MS", 40),
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envSeconds(key string, fallback int) time.Duration {
	return time.Duration(envInt(key, fallback)) * time.Second
}

func envMillis(key string, fallback int) time.Duration {
	return time.Duration(envInt(key, fallback)) * time.Millisecond
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("⚠️  %s=%q inválido, usando %d", key, v, fallback)
		return fallback
	}
	return n
}

func main() {
	cfg := loadConfig()

	game := NewGame(cfg.GameDuration, cfg.MinHitInterval)

	hub := NewHub(cfg.BroadcastInterval, func() []byte {
		data, err := json.Marshal(game.Snapshot(false, ""))
		if err != nil {
			return []byte(`{"error":"could not serialize state"}`)
		}
		return data
	})

	hub.Heartbeat = func() bool { return game.Active(time.Now()) }

	game.onChange = func(immediate bool) {
		if immediate {
			hub.Broadcast()
		} else {
			hub.MarkDirty()
		}
	}

	server := &Server{cfg: cfg, game: game, hub: hub}

	// ctx se cancela al recibir SIGTERM/SIGINT; cierra el ticker del hub
	// y, a través de BaseContext, todas las conexiones SSE abiertas.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go hub.Run(ctx)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("🎮 F13 Kind Game running on %s", httpServer.Addr)
	log.Printf("⏱️  Game duration: %s", cfg.GameDuration)
	if cfg.HostToken == "" {
		log.Printf("⚠️  HOST_TOKEN vacío: /api/start y /api/reset están abiertos a cualquiera")
	}

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}

	log.Printf("👋 Server stopped")
}
