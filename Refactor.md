# Plan de refactor — F13 Kind Game

Análisis del proyecto realizado el 11 de septiembre de 2026 (modelo Claude Fable 5.1).
El evento F13 Code Summit es el **12 y 13 de septiembre de 2026**, así que el plan está
ordenado por lo que aporta más con el menor riesgo antes del evento.

## Estado (11/09/2026)

| Fase | Estado |
|------|--------|
| Fase 0 | ✅ Aplicada completa (0.1 – 0.7) |
| Fase 1 | ✅ Aplicada completa (1.1 – 1.6) |
| Fase 2 | ✅ 2.1 división en archivos · 2.2 pruebas (15, `-race`) · 2.3 timeouts y apagado limpio · 2.4 frontend · 2.5 repo y k8s · ⏸️ 2.6 escalado (no necesario) |

Verificado en local con Podman: 16 escenarios de API, prueba k6 con 500 jugadores
(165 502 peticiones, 0 errores, p95 = 4,9 ms, 22 MB de RAM) y flujo completo en Chrome
(host con QR, join, START, toques, recarga con sesión recuperada, reset).

### Identidad visual (añadido el 11/09/2026)

Se alineó la interfaz con https://www.f13.pe/ tomando de la web:
- Logo oficial de la 2.ª edición (`static/assets/f13-logo.png`, 21 KB) y el motivo de
  cuadrados en píxel (favicon SVG y decoración en cabecera, arena y pie).
- Paleta: lima `#c7f241`, gris `#262626`/`#171717`, teal `#6accc2`, rosa `#e62b6e`,
  morado `#9e80e5`, verde `#02926c`, cian `#00f2de`.
- Tipografías Space Grotesk (texto) y Space Mono (títulos, etiquetas, números), OFL,
  autoalojadas en `static/fonts/` (~500 KB) para no depender de Internet en el evento.
- Botones pill sin borde grueso, tarjetas oscuras con texto blanco y acentos teal/lima,
  sin scanlines ni tramas.

### Arena con autoscaling simulado (añadido el 11/09/2026)

La arena del jugador pasó de una escena con el gopher a un mini cluster que reproduce
el diagrama de la charla: **Cliente (web del jugador) → Kubernetes Cluster [Gateway
(kgateway) + HTTPRoute → Service stable 90 % / canary 10 % → Pods v1 / v2]**. En el móvil
el botón "TOCA PARA ESCALAR" va antes del cluster, siempre visible, y el diagrama se lee de
arriba abajo; en el host ocupa la tercera columna de la rejilla y el QR con la URL está en
la cabecera. Los pods llevan nombres divertidos con sabor local (`cuy-turbo`,
`ceviche-picante`…) en tarjetas legibles desde el proyector. El markup de ambos clusters lo genera
`clusterTemplate()` en `app.js` y los grupos (peso y máximo de pods) están en `GROUPS`.
Cada toque es una request (partícula, lima para stable y rosa para canary) que salta
por cliente, gateway, route y service hasta un pod; cada grupo escala con su parte del
tráfico; un HPA en el cliente calcula req/s en una
ventana de 2 s y escala `deploy/tap-game` entre 1 y 12 réplicas (objetivo 2,5 req/s por
pod). Sube una réplica cada 350 ms con estados `creating → running`, y baja una cada
1,2 s tras 1,5 s sin tráfico. Muestra req/s, réplicas, CPU y el último evento estilo
`kubectl get events`. Es solo visual: el servidor sigue contando puntos. Los parámetros
están al inicio del objeto `cluster` en `static/app.js`.

La pantalla del host muestra el mismo cluster alimentado por el tráfico de toda la sala:
el servidor añade `hitsPerSecond` al snapshot (toques aceptados en los últimos 2 s,
contados en 20 buckets de 100 ms, sin guardar timestamps) y el HPA del host adapta el
objetivo por pod al número de jugadores (`max(3, jugadores × 6 / 12)`) para que llegue a
12 réplicas cuando todos tocan. `createCluster()` en `static/app.js` crea ambas
instancias (local para el jugador, remota para el host). El host también muestra el
temporizador de la ronda.

Decisiones tomadas al aplicar el plan que no estaban en el texto original:
- `Online` lo activa la conexión SSE (`?id=`), no el join: un bot que solo llame a la
  API no cuenta como conectado. El host muestra "N CONECTADOS · M registrados".
- El reset del host pide doble toque en 3 s en vez de `confirm()`.
- El `load-test.js` une a los jugadores primero y un escenario `host` pulsa START a los
  3 s, porque ahora nadie puede unirse con la partida en curso.
- Rutas con métodos de Go 1.22 (`POST /api/join`) y un `/api/` catch-all que responde
  JSON 404 para que ningún error de la API caiga en el `FileServer`.
- QR generado en el servidor con `github.com/skip2/go-qrcode` (única dependencia).

## Resumen del proyecto

| Capa | Tecnología | Archivos |
|------|------------|----------|
| Backend | Go 1.23, solo librería estándar, estado en memoria, SSE | `main.go` (609 líneas) |
| Frontend | HTML + CSS + JS sin frameworks | `static/index.html`, `static/app.js`, `static/style.css` |
| Infra | Dockerfile multi-stage, Deployment + Service (1 réplica), Ingress con cert-manager | `Dockerfile`, `k8s/` |
| Pruebas | Carga con k6, 500 VUs × 30 s | `load-test.js` |

Flujo: los jugadores entran desde el móvil (`/`), el host abre `/#host`, pulsa START,
todos tocan durante 30 s y el host ve el Top 5 en vivo. No hay pruebas unitarias.

La arquitectura es adecuada para una demo: un solo binario, sin dependencias, fácil de
explicar en escenario. Los problemas están en los detalles, no en el diseño.

---

## Fase 0 — Antes del evento (imprescindible, ~2 h)

Cambios pequeños, de bajo riesgo, que evitan que la demo falle en vivo.

### 0.1 Alinear puerto y duración con variables de entorno
- **Problema:** el puerto `:443` está fijo en `main.go:594` y sirve HTTP plano (no TLS).
  `GAME_DURATION` aparece en README, `Dockerfile:11` y `deployment.yaml:18-19`, pero el
  código nunca la lee (`main.go:17` es una constante).
- **Cambio:** leer `PORT` (por defecto `8080`) y `GAME_DURATION` (por defecto `30`) en
  `main()` con `os.Getenv`. Convertir `gameDuration` en campo del `Server`.
- **Efecto:** el README, el Dockerfile y los manifiestos vuelven a ser verdad.

### 0.2 Corregir la imagen y los puertos en los manifiestos
- **Problema:** README construye `kind-game:local`, pero `deployment.yaml:15` usa
  `kind-game:latest` → `ImagePullBackOff` en Kind. El `port-forward` del README apunta a
  `80`, pero el Service expone `443`.
- **Cambio:** unificar tag `kind-game:local`, Service `port: 80 → targetPort: 8080`,
  contenedor y probes en `8080`. Actualizar README e Ingress (`port.number: 80`).

### 0.3 Proteger `/api/start` y `/api/reset`
- **Problema:** cualquier asistente puede reiniciar la partida con un `curl`. El modo
  host solo depende de `#host` en la URL (`app.js:199`).
- **Cambio:** variable `HOST_TOKEN`; el host lo pasa en la URL (`/#host=<token>`) y el
  frontend lo envía en la cabecera `X-Host-Token`. Sin token válido → `403`.
  Si `HOST_TOKEN` está vacío, no se exige (modo desarrollo).

### 0.4 Rechazar `join` durante una partida en el servidor
- **Problema:** la restricción existe solo en el cliente (`app.js:19`). Un jugador que
  entre tarde compite con menos tiempo y distorsiona el Top 5.
- **Cambio:** en `join`, si `s.game.Status == "running"` → `409` con mensaje
  `"game already running"`. Mostrar el toast correspondiente en el cliente.

### 0.5 Truncado UTF-8 de nombres
- **Problema:** `name[:20]` en `main.go:225` corta por bytes. Verificado: un nombre con
  `ñ` en el corte se guarda como `"aaa…a�"`.
- **Cambio:** truncar por runas (`[]rune(name)[:20]`) y validar que el nombre no esté
  vacío tras el `TrimSpace`.

### 0.6 Recuperar al jugador tras un reset o una recarga
- **Problema:** tras `reset` el móvil conserva `me.id`, cada toque devuelve `404` en
  silencio y el jugador debe recargar. El `localStorage` de `app.js:43-44` se escribe pero
  nunca se lee.
- **Cambio:**
  - En `hit()`, si la respuesta es `404`, volver a la pantalla de join con un toast
    ("La partida se reinició, vuelve a entrar").
  - En `render()`, si `me` no aparece en `players`, hacer lo mismo.
  - Al cargar, leer `kindGamePlayerId` de `localStorage` y, si el jugador sigue en el
    estado, restaurar la sesión sin pedir el nombre de nuevo.

### 0.7 Mostrar un QR real en la pantalla del host
- **Problema:** el pie del host dice "Escanea el QR" (`index.html:317`) pero solo muestra
  la URL en texto.
- **Cambio:** generar el QR en el servidor (`/api/qr.png` con `github.com/skip2/go-qrcode`)
  o en el cliente con una librería mínima inline. La opción servidor mantiene el
  frontend sin dependencias externas y funciona sin acceso a CDN en el lugar del evento.

---

## Fase 1 — Robustez bajo carga (recomendado antes del evento, ~2 h)

### 1.1 Emitir el estado por intervalos en vez de por cada punto
- **Problema:** cada `score` aceptado dispara `broadcast()` (`main.go:316`), que ordena
  y serializa a **todos** los jugadores y lo envía a **todas** las conexiones SSE.
  Con 500 jugadores × 10 toques/s → ~5000 snapshots/s de 500 jugadores cada uno.
- **Cambio:** una goroutine `ticker` (cada 150 ms) que emite solo si hubo cambios
  (flag `dirty`). `start`, `reset` y fin de partida siguen emitiendo de inmediato.
- **Efecto:** el coste pasa a ser constante e independiente del número de toques.

### 1.2 Reducir el payload del SSE
- **Problema:** el snapshot incluye `players` completo y `top` (duplicado). El móvil solo
  usa su propio score y el estado del juego; el host solo usa `top` y el contador.
- **Cambio:** enviar `game`, `top`, `playerCount` y, opcionalmente, un mapa
  `id → score` compacto. Eliminar `players` del evento (mantenerlo en `/api/state` si se
  quiere para depurar).

### 1.3 Límite de peticiones por jugador en `/api/score`
- **Problema:** sin límite, un bucle de `curl` gana siempre. El público será de
  desarrolladores.
- **Cambio:** rechazar toques a menos de ~40 ms del anterior por jugador (guardar
  `lastHitAt` en `Player`). Es más simple que un token bucket y suficiente para una
  demo. Documentar el límite en el README como "regla del juego".

### 1.4 Marcar `online` de verdad
- **Problema:** `Online` siempre es `true` (`main.go:256`). El contador del host
  muestra registrados, no conectados.
- **Cambio:** asociar cada conexión SSE a un `playerId` (query `?id=`) y poner
  `Online=false` al cerrarse. O eliminar el campo y renombrar el contador a "JUGADORES
  REGISTRADOS". La segunda opción es la más honesta con el menor esfuerzo.

### 1.5 Timer resistente al desfase de reloj del móvil
- **Problema:** `updateTimer` compara `endsAt` del servidor con `Date.now()` del móvil
  (`app.js:169`). Un teléfono con reloj desfasado ve un contador incorrecto.
- **Cambio:** incluir `serverNow` en el snapshot; el cliente calcula el offset una vez y
  lo aplica a `endsAt`.

### 1.6 Arreglar el load test
- **Problema:** ejecutado dos veces sin reset todos los `join` devuelven `409` y
  `join.json()` lanza. Los puntos solo cuentan si alguien pulsó START a mano.
- **Cambio:** nombre `LoadTest-${__VU}-${__ITER}`, comprobar `join.status` antes de leer
  el cuerpo, y un escenario `setup()` que llame a `/api/reset` y `/api/start` con el
  token de host.

---

## Fase 2 — Calidad y mantenimiento (después del evento)

### 2.1 Reorganizar `main.go`
Dividir en `game.go` (estado y reglas), `handlers.go` (HTTP), `sse.go` (broadcast) y
`main.go` (wiring). Mantener un solo paquete `main` para no complicar el build.

### 2.2 Pruebas
- Unitarias para `Game`: join duplicado, join durante partida, score fuera de tiempo,
  fin de partida no cierra una partida nueva (`expectedEnd`), truncado de nombres.
- Un test de `httptest` para el flujo join → start → score → finished.
- Un test de carrera con `go test -race`.

### 2.3 Apagado limpio y timeouts HTTP
`http.ListenAndServe` sin timeouts (`main.go:607`). Usar `http.Server` con
`ReadHeaderTimeout` y `context` para cerrar las conexiones SSE al recibir `SIGTERM`
(Kubernetes envía SIGTERM en cada rollout).

### 2.4 Frontend
- Eliminar `#joined` (`index.html:90`), nunca se usa.
- Unificar el rango de nombres aleatorios: frontend usa `10–99`, backend `1–99`.
- Añadir `@media (prefers-reduced-motion)` para desactivar las animaciones del arena.
- Comprimir `golang.png` (1,3 MB → <100 KB con WebP). Se descarga en cada móvil, y la
  red del evento suele ser el punto débil.

### 2.5 Repositorio
- Añadir `.gitignore` y quitar los `.DS_Store` versionados (`git rm --cached`).
- Fijar la versión de Go en `go.mod` (`go 1.23.x`) y usar `golang:1.23.12-alpine` en el
  Dockerfile para builds reproducibles.
- Añadir `securityContext` (usuario no root, `readOnlyRootFilesystem`) y `resources`
  al Deployment. El binario estático no necesita permisos.

### 2.6 Escalado horizontal (solo si hace falta)
El README ya lo documenta: para más de una réplica hace falta Redis (estado + Pub/Sub).
No merece la pena para la demo; 1 réplica con la Fase 1 aplicada aguanta cientos de
jugadores sin problema.

---

## Orden sugerido de ejecución

```
Fase 0:  0.1 → 0.2 → 0.5 → 0.4 → 0.3 → 0.6 → 0.7
         (los tres primeros son cambios de configuración; probar en Kind después de 0.2)
Fase 1:  1.1 → 1.2 → 1.3 → 1.6 (correr k6 para validar) → 1.4 → 1.5
Fase 2:  2.5 → 2.1 → 2.2 → 2.3 → 2.4
```

## Verificación en local

Go no está instalado en esta máquina; la app se construye y corre con Podman:

```bash
podman build -t kind-game:local .
podman run -d --name kind-game -p 8080:443 kind-game:local   # tras 0.1: -p 8080:8080
curl localhost:8080/api/state
```

Estado verificado el 11/09/2026: join, duplicados (409), score antes/durante partida,
start, reset, SSE e index funcionan. Confirmado el bug de truncado UTF-8 (0.5).
