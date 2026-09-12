# F13 Game — Backstage + Crossplane Platform Demo

Esta demo cuenta una historia completa de Platform Engineering alrededor de un pequeño juego multiplayer en Go para Kubernetes/Kind.

La idea es mostrar primero **la aplicación funcionando**, después **cómo Kubernetes la ejecuta y escala visualmente**, y finalmente **cómo Backstage abstrae el provisionamiento cloud con Crossplane**. Por tiempo, **Crossplane se muestra como parte de la arquitectura y se revisan sus manifiestos, pero no se ejecuta durante la charla**.

## Orden de la demo

### 1. La aplicación: multiplayer arcade

La aplicación Go permite que varios teléfonos se conecten al mismo servidor. Un host inicia una ronda global, todos los jugadores puntúan al mismo tiempo y la pantalla principal muestra el Top 5 en tiempo real.

Puntos clave para explicar:

- Estado del juego en memoria.
- Un único proceso/replica para mantener una sola partida.
- SSE para enviar el estado en tiempo real a todos los navegadores.
- Protección del host mediante `HOST_TOKEN`.
- Anti-script mediante `MIN_HIT_INTERVAL_MS`.
- Endpoint `/healthz` para Kubernetes.

## 2. Ejecutar localmente

```bash
go run .
# o

docker build -t kind-game:local . && docker run --rm -p 8080:8080 kind-game:local
```

- Jugadores: `http://localhost:8080/`
- Host: `http://localhost:8080/#host`

## 3. Desplegar la app en Kind

```bash
kind create cluster --name game

docker build -t kind-game:local .
kind load docker-image kind-game:local --name game

kubectl create secret generic kind-game \\
  --from-literal=hostToken=$(openssl rand -hex 8)

kubectl apply -f k8s/deployment.yaml
kubectl port-forward svc/kind-game 8080:80
```

En esta parte se enseña Kubernetes de forma visible: Deployment, Service, Secret y, según el escenario, Ingress.

> Para teléfonos en la misma Wi‑Fi, el servicio debe quedar accesible desde la red local. `kubectl port-forward` sirve para pruebas en la misma máquina; para el evento conviene usar NodePort, Ingress o un túnel accesible desde la LAN.

## 4. La parte que hace "wow": simulación de autoscaling

La pantalla del juego representa un mini cluster Kubernetes y muestra visualmente el tráfico entrando al Gateway y llegando a los pods.

```text
Client
  │
  ▼
Kubernetes Cluster
  │
  ▼
Gateway (kgateway) + HTTPRoute
  ├── 90% → Service stable → Pods v1
  └── 10% → Service canary → Pods v2
```

Los taps generan tráfico y la interfaz simula:

- HPA por request/sec.
- Incremento y reducción de replicas.
- Pods pasando de `ContainerCreating` a `Running`.
- CPU por pod.
- Eventos estilo Kubernetes.
- Canary 90/10.
- Escalamiento del cluster según los jugadores conectados.

Importante: esta parte es principalmente **visual para la experiencia de la charla**; el servidor del juego sigue concentrado en el conteo de puntos.

## 5. Conectar la historia con Platform Engineering

Aquí aparece la pregunta natural:

> "¿Y quién crea todo esto para un developer?"

La respuesta de la demo es **Backstage**.

El developer no tiene que conocer todos los YAML ni todos los detalles de cada cloud. En Backstage selecciona una intención de plataforma, por ejemplo:

```text
Application: f13-game
Team: platform-team
Environment: demo
Cloud: Azure / AWS / GCP
Network Policy: ON
Private Cluster: ON
Autoscaling: ON
Vault: ON
```

## 6. Backstage → Crossplane

Backstage Scaffolder genera un repositorio con:

- manifiestos Kubernetes del workload;
- guardrails de plataforma;
- manifestos Crossplane para el cloud seleccionado;
- configuración preparada para Vault;
- `catalog-info.yaml` para registrar el componente en Backstage.

La arquitectura conceptual es:

```text
Developer
   │
   ▼
Backstage
   │
   │ Scaffolder
   ▼
GitHub Repository
   │
   ├── Kubernetes manifests
   └── Crossplane manifests
          │
          ▼
      Crossplane
       ├── Azure → AKS
       ├── AWS   → EKS
       └── GCP   → GKE
```

## 7. Crossplane — solo se muestra, no se ejecuta

**Por tiempo, esta parte NO se ejecuta en vivo.**

La demostración termina mostrando los manifiestos generados y explicando el contrato:

> "Este YAML no es Terraform. Es una API declarativa de Kubernetes. Backstage genera la intención y Crossplane sería quien reconciliaría esa intención contra Azure, AWS o GCP."

Se pueden abrir rápidamente estos archivos:

```text
aks/crossplane/cluster.yaml
eks/crossplane/cluster.yaml
gke/crossplane/cluster.yaml
crossplane/provider-configs/azure.yaml
crossplane/provider-configs/aws.yaml
crossplane/provider-configs/gcp.yaml
```

No se requiere instalar providers ni crear infraestructura cloud durante la charla.

## 8. Vault como capa de secretos

La plataforma asume un Vault disponible en:

`https://hashicorpvault.josua.com.pe`

Los manifests de ejemplo dejan preparada la integración mediante los recursos de autenticación/secretos de Vault.

La idea para explicar es:

```text
Backstage
   │
   ▼
GitHub
   │
   ▼
Kubernetes
   │
   ▼
Vault
   │
   ▼
Application Secret
```

No se colocan secretos reales dentro del repositorio.

## 9. Mensaje final

La historia de la demo queda resumida así:

```text
Backstage = Developer Experience
Crossplane = Infrastructure Orchestration
Kubernetes = Runtime
Vault = Secrets
GitHub = Source of Truth
```

### Frase para cerrar

> **"El developer pide una capacidad. Backstage le da el camino dorado. Kubernetes ejecuta el workload. Crossplane puede encargarse de la infraestructura y Vault de los secretos. La complejidad queda detrás de la plataforma."**

## Configuración principal de la app

| Variable | Default | Descripción |
|----------|---------|-------------|
| `PORT` | `8080` | Puerto HTTP |
| `GAME_DURATION` | `30` | Duración de la ronda |
| `HOST_TOKEN` | *(vacío)* | Protege START/RESET |
| `BROADCAST_INTERVAL_MS` | `150` | Frecuencia de broadcast |
| `MIN_HIT_INTERVAL_MS` | `40` | Intervalo mínimo entre taps contados |
| `STATIC_DIR` | `./static` | Directorio frontend |

## Cloud Run (opcional)

La app también puede publicarse en Cloud Run, pero para esta charla el foco principal es **Kind + Kubernetes + Backstage + Crossplane**.

Como el estado está en memoria, cualquier despliegue que requiera una única partida debe mantener una sola instancia. Para una evolución real a múltiples replicas habría que mover el estado y la propagación de eventos a una capa compartida, por ejemplo Redis.
