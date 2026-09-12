# KIND Game — multiplayer demo

A small multiplayer arcade for a Kubernetes/Kind demo. Multiple phones connect to the same Go server. The host starts one global round; all players score concurrently; the big screen shows the live Top 5.

## Run locally

```bash
go run .
# or, without Go installed:
docker build -t kind-game:local . && docker run --rm -p 8080:8080 kind-game:local
```

- Players: `http://localhost:8080/`
- Host (big screen): `http://localhost:8080/#host`

Phones on the same Wi‑Fi can use `http://<YOUR-LAN-IP>:8080/`. The host screen shows a QR code with that URL in the header.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP port |
| `GAME_DURATION` | `30` | Round length in seconds |
| `HOST_TOKEN` | *(empty)* | If set, `/api/start` and `/api/reset` require it. The host opens `/#host=<token>`. **Set it for the live event.** |
| `BROADCAST_INTERVAL_MS` | `150` | How often the state is pushed to browsers |
| `MIN_HIT_INTERVAL_MS` | `40` | Minimum time between two counted taps per player (anti‑script) |
| `STATIC_DIR` | `./static` | Frontend directory |

## Build and run in Kind

```bash
kind create cluster --name game

docker build -t kind-game:local .
kind load docker-image kind-game:local --name game

kubectl create secret generic kind-game --from-literal=hostToken=$(openssl rand -hex 8)
kubectl apply -f k8s/deployment.yaml
kubectl port-forward svc/kind-game 8080:80
```

Then:
- Big screen: `http://localhost:8080/#host=<token>`
- Phones: `http://<YOUR-LAN-IP>:8080/`

For phones to reach the service, expose it through your laptop's LAN IP or an ingress/tunnel. `kubectl port-forward` is convenient for testing from the same machine, but phones on the LAN normally need a reachable host/NodePort/Ingress. `k8s/ingress.yaml` has an example with cert‑manager and the nginx annotations SSE needs.

## Publish the image and deploy to Cloud Run

The image must be `linux/amd64` for Cloud Run. The Dockerfile cross‑compiles, so it builds on Apple Silicon without emulation:

```bash
podman build --platform linux/amd64 -t docker.io/rcronald/f13-game:latest .
podman push docker.io/rcronald/f13-game:latest
```

Cloud Run settings that matter for this app (state lives in memory and every phone keeps an SSE connection open):

| Setting | Value | Why |
|---------|-------|-----|
| Container port | `8080` | the app also honours `$PORT` |
| Env `HOST_TOKEN` | a secret | protects START/RESET |
| Min / max instances | `1` / **`1`** | one game state; two instances = two games |
| Max concurrent requests per instance | `1000` | each player holds one SSE request; default 80 would cap the room |
| Request timeout | `3600` s | SSE streams are long requests |
| CPU allocation | always allocated | round timer and broadcaster run between requests |
| Authentication | allow unauthenticated | players and host are anonymous |

Equivalent CLI:

```bash
gcloud run deploy f13-game --image docker.io/rcronald/f13-game:latest \
  --port 8080 --set-env-vars HOST_TOKEN=secreto \
  --min-instances 1 --max-instances 1 --concurrency 1000 --timeout 3600 \
  --no-cpu-throttling --allow-unauthenticated --region us-central1
```

## Game rules

- Players join with a name or random name (max 20 characters, unique, case‑insensitive).
- Nobody can join while a round is running.
- Host clicks START; every phone starts at the same time.
- Each tap adds one point. Taps faster than `MIN_HIT_INTERVAL_MS` are ignored.
- Score updates are pushed to every connected browser using Server‑Sent Events (SSE).
- After the round, the Top 5 stays on the host screen. START again for a new round (scores reset, players stay). RESET clears everyone.

## API

| Method | Path | Notes |
|--------|------|-------|
| `POST` | `/api/join` | `{"name": "..."}` → player + `epoch`. `409 name_taken` / `409 game_running` |
| `POST` | `/api/score?id=` | `{"score", "accepted"}`. `404` if the player no longer exists |
| `POST` | `/api/start` | host only |
| `POST` | `/api/reset` | host only |
| `GET` | `/api/state?id=` | full snapshot; with `id`, includes `me` |
| `GET` | `/api/events?id=` | SSE stream (light snapshot: game, top 5, counters, `hitsPerSecond`) |
| `GET` | `/api/qr.png?url=` | QR for the host screen |
| `GET` | `/healthz` | probes |

## Tests and load test

```bash
go test -race ./...
k6 run load-test.js                       # 500 players × ~10 taps/s
k6 run -e HOST_TOKEN=secret load-test.js  # when HOST_TOKEN is set
```

## Autoscaling simulation (player screen)

The arena is a mini cluster drawn like the diagram used in the talk, so a Kubernetes audience recognises it at a glance:

```
Client (the player's web)
  └─ Kubernetes Cluster (kind)
       Gateway (kgateway) + HTTPRoute
         ├─ Service stable (90 %) → Pods v1
         └─ Service canary (10 %) → Pods v2
```

Every accepted tap is a request that hops client → gateway → route → service → pod (the links animate while there is traffic); the HTTPRoute sends 90 % to stable and 10 % to canary, and each group scales with its share of the traffic (v1 up to 9 pods, v2 up to 3). Pods get fun local names (`cuy-turbo`, `ceviche-picante`, `llama-ninja`…) from the `POD_NOUNS` / `POD_ADJECTIVES` lists. On phones the tap button sits above the cluster so it is always visible; the diagram reads top‑down on both screens. A client‑side HPA computes req/s over a 2 s window and scales each group (target 2.5 req/s per pod, 12 pods in total): it scales up one pod every 350 ms while load is high (pods appear as `ContainerCreating` and then `Running`), and scales down one pod every 1.2 s after 1.5 s without traffic. CPU per pod, replicas and the last Kubernetes‑style event are shown live. On the player screen it is purely visual — the server only counts points. The host screen shows the same cluster fed by the **whole room**: the server reports `hitsPerSecond` (accepted taps over the last 2 s, counted in 100 ms buckets) in every snapshot, and the host HPA adapts its per‑pod target to the number of players (`max(3, players × 6 / 12)`) so the cluster hits 12 replicas when everyone taps. Tunables live in `createCluster()` in `static/app.js`.

## Brand

The UI follows the identity of [f13.pe](https://www.f13.pe/): lime `#c7f241` on dark `#262626`, accents teal `#6accc2`, pink `#e62b6e` and purple `#9e80e5`, Space Grotesk for body text and Space Mono for headings, labels and numbers (both OFL, self‑hosted in `static/fonts/` so the game works offline at the venue). The official 2nd‑edition logo lives in `static/assets/f13-logo.png` and the pixel‑square motif is reused as favicon and decoration.

## Important demo limitation

State is intentionally in memory and the deployment is one replica. This is ideal for a live Kind demo. If you want to scale to multiple replicas, add Redis for shared scores/game state and Redis Pub/Sub (or a WebSocket/SSE gateway) so every pod broadcasts the same events.
