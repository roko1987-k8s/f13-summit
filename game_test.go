package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestGame(t *testing.T) *Game {
	t.Helper()
	return NewGame(time.Hour, 0)
}

func TestJoinRejectsDuplicateNamesIgnoringCaseAndSpaces(t *testing.T) {
	g := newTestGame(t)

	if _, err := g.Join("Ana"); err != nil {
		t.Fatalf("first join: %v", err)
	}

	if _, err := g.Join("  ANA "); err != ErrNameExists {
		t.Fatalf("want ErrNameExists, got %v", err)
	}
}

func TestJoinAssignsRandomNameWhenEmpty(t *testing.T) {
	g := newTestGame(t)

	p, err := g.Join("   ")
	if err != nil || p.Name == "" {
		t.Fatalf("want random name, got %q err=%v", p.Name, err)
	}
}

func TestJoinTruncatesByRunesNotBytes(t *testing.T) {
	g := newTestGame(t)

	name := strings.Repeat("a", 19) + "ñb" // 21 runas, 22 bytes

	p, err := g.Join(name)
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Repeat("a", 19) + "ñ"
	if p.Name != want {
		t.Fatalf("want %q, got %q", want, p.Name)
	}
}

func TestJoinRejectedWhileRunning(t *testing.T) {
	g := newTestGame(t)
	g.Start()

	if _, err := g.Join("late"); err != ErrGameRunning {
		t.Fatalf("want ErrGameRunning, got %v", err)
	}
}

func TestHitOnlyCountsWhileRunning(t *testing.T) {
	g := newTestGame(t)
	p, _ := g.Join("ana")

	if score, accepted, _ := g.Hit(p.ID); accepted || score != 0 {
		t.Fatalf("hit in lobby: accepted=%v score=%d", accepted, score)
	}

	g.Start()

	if score, accepted, _ := g.Hit(p.ID); !accepted || score != 1 {
		t.Fatalf("hit while running: accepted=%v score=%d", accepted, score)
	}

	if _, _, err := g.Hit("nope"); err != ErrNotFound {
		t.Fatalf("unknown player: want ErrNotFound, got %v", err)
	}
}

func TestHitRateLimit(t *testing.T) {
	g := NewGame(time.Hour, 50*time.Millisecond)
	p, _ := g.Join("ana")
	g.Start()

	if _, accepted, _ := g.Hit(p.ID); !accepted {
		t.Fatal("first hit should be accepted")
	}
	if _, accepted, _ := g.Hit(p.ID); accepted {
		t.Fatal("second immediate hit should be rate limited")
	}

	time.Sleep(60 * time.Millisecond)

	if score, accepted, _ := g.Hit(p.ID); !accepted || score != 2 {
		t.Fatalf("hit after interval: accepted=%v score=%d", accepted, score)
	}
}

func TestStartResetsScoresAndFinishes(t *testing.T) {
	g := NewGame(30*time.Millisecond, 0)
	p, _ := g.Join("ana")

	g.Start()
	g.Hit(p.ID)

	if snap := g.Snapshot(false, ""); snap.Top[0].Score != 1 {
		t.Fatalf("want score 1, got %d", snap.Top[0].Score)
	}

	time.Sleep(60 * time.Millisecond)

	if snap := g.Snapshot(false, ""); snap.Game.Status != statusFinished {
		t.Fatalf("want finished, got %s", snap.Game.Status)
	}

	g.Start()

	if snap := g.Snapshot(false, ""); snap.Top[0].Score != 0 || snap.Game.Status != statusRunning {
		t.Fatalf("new round: score=%d status=%s", snap.Top[0].Score, snap.Game.Status)
	}
}

func TestFinishDoesNotCloseANewerRound(t *testing.T) {
	g := NewGame(time.Hour, 0)

	first := g.Start()
	time.Sleep(2 * time.Millisecond)
	g.Start() // segunda ronda con otro EndsAt

	g.finish(first.EndsAt)

	if snap := g.Snapshot(false, ""); snap.Game.Status != statusRunning {
		t.Fatalf("stale finish closed the new round: %s", snap.Game.Status)
	}
}

func TestResetChangesEpochAndClearsPlayers(t *testing.T) {
	g := newTestGame(t)
	g.Join("ana")
	before := g.Epoch()

	time.Sleep(2 * time.Millisecond)
	g.Reset()

	if g.Epoch() == before {
		t.Fatal("epoch should change on reset")
	}
	if snap := g.Snapshot(true, ""); snap.PlayerCount != 0 || snap.Game.Status != statusLobby {
		t.Fatalf("after reset: players=%d status=%s", snap.PlayerCount, snap.Game.Status)
	}
}

func TestPresence(t *testing.T) {
	g := newTestGame(t)
	p, _ := g.Join("ana")

	g.Connect(p.ID)
	g.Connect(p.ID)
	g.Disconnect(p.ID)

	if snap := g.Snapshot(false, ""); snap.OnlineCount != 1 {
		t.Fatalf("one tab still open: online=%d", snap.OnlineCount)
	}

	g.Disconnect(p.ID)

	if snap := g.Snapshot(false, ""); snap.OnlineCount != 0 {
		t.Fatalf("all tabs closed: online=%d", snap.OnlineCount)
	}
}

func TestSnapshotTopAndMe(t *testing.T) {
	g := newTestGame(t)

	var ids []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		p, _ := g.Join(n)
		ids = append(ids, p.ID)
	}

	g.Start()
	for i := 0; i < 3; i++ {
		g.Hit(ids[6]) // "g" gana
	}

	snap := g.Snapshot(false, ids[6])

	if len(snap.Top) != topSize {
		t.Fatalf("want top %d, got %d", topSize, len(snap.Top))
	}
	if snap.Top[0].Name != "g" || snap.Top[0].Score != 3 {
		t.Fatalf("want g=3 first, got %s=%d", snap.Top[0].Name, snap.Top[0].Score)
	}
	if snap.Players != nil {
		t.Fatal("light snapshot must not include players")
	}
	if snap.Me == nil || snap.Me.Score != 3 {
		t.Fatalf("want me with score 3, got %+v", snap.Me)
	}
	if snap.PlayerCount != 7 {
		t.Fatalf("want 7 players, got %d", snap.PlayerCount)
	}
}

func TestSnapshotHitsPerSecond(t *testing.T) {
	g := NewGame(time.Hour, 0)
	a, _ := g.Join("a")
	b, _ := g.Join("b")
	g.Start()

	if snap := g.Snapshot(false, ""); snap.HitsPerSecond != 0 {
		t.Fatalf("no hits yet: %v", snap.HitsPerSecond)
	}

	for i := 0; i < 5; i++ {
		g.Hit(a.ID)
		g.Hit(b.ID)
	}

	// 10 toques en una ventana de 2 s → 5 req/s.
	if snap := g.Snapshot(false, ""); snap.HitsPerSecond != 5 {
		t.Fatalf("want 5 req/s, got %v", snap.HitsPerSecond)
	}

	// Un toque ignorado por rate limit no cuenta.
	g2 := NewGame(time.Hour, time.Second)
	p, _ := g2.Join("p")
	g2.Start()
	g2.Hit(p.ID)
	g2.Hit(p.ID)
	if snap := g2.Snapshot(false, ""); snap.HitsPerSecond != 0.5 {
		t.Fatalf("want 0.5 req/s, got %v", snap.HitsPerSecond)
	}
}

func TestActiveWhileRunningAndBrieflyAfterFinish(t *testing.T) {
	g := NewGame(30*time.Millisecond, 0)

	if g.Active(time.Now()) {
		t.Fatal("lobby should not be active")
	}

	g.Start()
	if !g.Active(time.Now()) {
		t.Fatal("running should be active")
	}

	time.Sleep(60 * time.Millisecond)
	now := time.Now()
	if !g.Active(now) {
		t.Fatal("just finished should still be active (draining req/s)")
	}
	if g.Active(now.Add(5 * time.Second)) {
		t.Fatal("long after finish should not be active")
	}
}

// ----------------------------------------------------
// HTTP
// ----------------------------------------------------

func newTestServer(t *testing.T, hostToken string) *httptest.Server {
	t.Helper()

	game := NewGame(time.Hour, 0)
	hub := NewHub(time.Hour, func() []byte { return []byte("{}") })

	s := &Server{
		cfg:  Config{HostToken: hostToken, StaticDir: t.TempDir()},
		game: game,
		hub:  hub,
	}

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestHostEndpointsRequireToken(t *testing.T) {
	ts := newTestServer(t, "secret")

	for _, path := range []string{"/api/start", "/api/reset"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, nil)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s without token: want 403, got %d", path, res.StatusCode)
		}

		req.Header.Set("X-Host-Token", "secret")
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s with token: want 200, got %d", path, res.StatusCode)
		}
	}
}

func TestHostEndpointsOpenWithoutConfiguredToken(t *testing.T) {
	ts := newTestServer(t, "")

	res, err := http.Post(ts.URL+"/api/start", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", res.StatusCode)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	ts := newTestServer(t, "")

	// Un GET a una ruta POST no debe caer en el FileServer.
	res, err := http.Get(ts.URL + "/api/join")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /api/join: want JSON 404, got %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

func TestStaticCacheHeaders(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"index.html", "app.js"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "logo.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{cfg: Config{StaticDir: dir}, game: NewGame(time.Hour, 0), hub: NewHub(time.Hour, func() []byte { return nil })}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	cases := map[string]string{
		"/":                "no-cache",
		"/app.js":          "no-cache",
		"/assets/logo.png": "public, max-age=86400",
	}
	for path, want := range cases {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got := res.Header.Get("Cache-Control"); got != want {
			t.Fatalf("%s: want Cache-Control %q, got %q", path, want, got)
		}
	}
}

func TestQRValidatesURL(t *testing.T) {
	ts := newTestServer(t, "")

	res, _ := http.Get(ts.URL + "/api/qr.png?url=javascript:alert(1)")
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad scheme: want 400, got %d", res.StatusCode)
	}

	res, _ = http.Get(ts.URL + "/api/qr.png?url=http://example.com/")
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("valid url: status=%d type=%s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}
