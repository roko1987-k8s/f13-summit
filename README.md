# KIND Game — multiplayer demo

A small multiplayer arcade for a Kubernetes/Kind demo. Multiple phones connect to the same Go server. The host starts one global round; all players score concurrently; the big screen shows the live Top 5.

## Run locally

```bash
go run .
```

Open `http://localhost:8080/` for players. Open `http://localhost:8080/#host` on the big screen. The host sees START.

## Build and run in Kind

```bash
kind create cluster --name game

docker build -t kind-game:local .
kind load docker-image kind-game:local --name game
kubectl apply -f k8s/deployment.yaml
kubectl port-forward svc/kind-game 8080:80
```

Then:
- Big screen: `http://localhost:8080/#host`
- Phones: `http://<YOUR-LAN-IP>:8080/`

For phones to reach the service, expose it through your laptop's LAN IP or an ingress/tunnel. `kubectl port-forward` is convenient for testing from the same machine, but phones on the LAN normally need a reachable host/NodePort/Ingress.

## Game rules

- Players join with a name or random name.
- Host clicks START.
- Default round is 30 seconds (`GAME_DURATION`).
- Each successful coin click adds one point.
- Score updates are pushed to every connected browser using Server-Sent Events (SSE).
- After the round, only Top 5 are highlighted.

## Important demo limitation

State is intentionally in memory and the deployment is one replica. This is ideal for a live Kind demo. If you want to scale to multiple replicas, add Redis for shared scores/game state and Redis Pub/Sub (or a WebSocket/SSE gateway) so every pod broadcasts the same events.
