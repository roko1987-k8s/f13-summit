let me = null; // { id, name, epoch }
let state = { game: { status: 'lobby', endsAt: 0, epoch: 0 }, top: [], playerCount: 0, onlineCount: 0, hitsPerSecond: 0 };
let serverOffset = 0; // serverNow - Date.now(), corrige el reloj del móvil
let timerInterval = null;
let eventSource = null;
let resetArmedUntil = 0;

const STORAGE_ID = 'kindGamePlayerId';

const $ = (id) => document.getElementById(id);

const randomNames = [
  'PixelGopher', 'KubeNinja', 'GoRunner', 'PodMaster', 'CloudFox',
  'ByteBear', 'KindHero', 'GopherAce', 'NodeWizard', 'ClusterKid',
  'GoPilot', 'TinyPod', 'KubeRider', 'PixelBot', 'CloudKid'
];

// ----------------------------------------------------
// MODO HOST
// ----------------------------------------------------

// El host abre /#host o /#host=<token>. El token viaja en la cabecera
// X-Host-Token de start y reset.
function hostMode() {
  const hash = location.hash.slice(1);
  if (!hash.toLowerCase().startsWith('host')) return null;
  const [, token = ''] = hash.split('=');
  return { token };
}

function hostHeaders() {
  const host = hostMode();
  return host && host.token ? { 'X-Host-Token': host.token } : {};
}

// ----------------------------------------------------
// JOIN / SESIÓN
// ----------------------------------------------------

function randomName() {
  $('name').value = `${randomNames[Math.floor(Math.random() * randomNames.length)]}-${Math.floor(Math.random() * 90) + 10}`;
}

async function join() {
  if (state.game.status === 'running') {
    toast('La partida ya comenzó. Espera la siguiente ronda.');
    return;
  }

  const name = $('name').value.trim();

  try {
    const response = await fetch('/api/join', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name })
    });

    if (response.status === 409) {
      const { error } = await response.json().catch(() => ({}));
      toast(error === 'game_running'
        ? 'La partida ya comenzó. Espera la siguiente ronda.'
        : 'Ya existe un jugador con ese nombre.');
      return;
    }

    if (!response.ok) throw new Error('join failed');

    const player = await response.json();
    enterGame(player, player.epoch);
    toast(`¡Bienvenido, ${player.name}!`);
  } catch (error) {
    toast('No pudimos registrarte. Intenta nuevamente.');
  }
}

function enterGame(player, epoch) {
  me = { id: player.id, name: player.name, epoch };

  try { localStorage.setItem(STORAGE_ID, me.id); } catch (e) { /* modo privado */ }

  $('playerName').textContent = me.name;
  $('score').textContent = player.score || 0;
  $('joinScreen').classList.add('hidden');
  $('gameScreen').classList.remove('hidden');

  updateGame(state.game);
  connectEvents(); // reconecta con ?id= para contar como online
}

function leaveGame(message) {
  me = null;

  try { localStorage.removeItem(STORAGE_ID); } catch (e) { /* modo privado */ }

  $('gameScreen').classList.add('hidden');
  $('joinScreen').classList.remove('hidden');
  $('tapButton').disabled = true;

  connectEvents();
  if (message) toast(message);
}

// Tras recargar la página, recupera la sesión si el jugador sigue en el servidor.
async function restoreSession() {
  let savedId = null;
  try { savedId = localStorage.getItem(STORAGE_ID); } catch (e) { /* modo privado */ }

  const url = savedId ? `/api/state?id=${encodeURIComponent(savedId)}` : '/api/state';

  try {
    const data = await fetch(url).then(r => r.json());
    render(data);

    if (data.me && !hostMode()) {
      enterGame(data.me, data.game.epoch);
    } else if (savedId) {
      try { localStorage.removeItem(STORAGE_ID); } catch (e) { /* modo privado */ }
    }
  } catch (error) {
    // Sin estado inicial; el SSE lo traerá.
  }
}

// ----------------------------------------------------
// HOST
// ----------------------------------------------------

async function startGame() {
  const response = await fetch('/api/start', { method: 'POST', headers: hostHeaders() });

  if (response.status === 403) {
    toast('Token de host inválido. Abre /#host=<token>.');
  } else if (!response.ok) {
    toast('No se pudo iniciar la partida.');
  }
}

// Doble toque para evitar un reset accidental en plena demo.
async function resetGame() {
  const button = $('resetBtn');

  if (Date.now() > resetArmedUntil) {
    resetArmedUntil = Date.now() + 3000;
    button.textContent = '¿SEGURO? TOCA DE NUEVO';
    setTimeout(() => { if (Date.now() >= resetArmedUntil) button.textContent = 'RESET'; }, 3000);
    return;
  }

  resetArmedUntil = 0;
  button.textContent = 'RESET';

  const response = await fetch('/api/reset', { method: 'POST', headers: hostHeaders() });

  if (response.status === 403) toast('Token de host inválido. Abre /#host=<token>.');
}

// ----------------------------------------------------
// TOQUE
// ----------------------------------------------------

async function hit() {
  if (!me || state.game.status !== 'running') return;

  const response = await fetch(`/api/score?id=${encodeURIComponent(me.id)}`, { method: 'POST' });

  if (response.status === 404) {
    leaveGame('La partida se reinició. Vuelve a entrar.');
    return;
  }

  if (!response.ok) return;

  const data = await response.json();

  $('score').textContent = data.score;

  if (data.accepted) {
    playerCluster.request();
    createTapEffect();
    cheerMascot();
    if (navigator.vibrate) navigator.vibrate(8);
  }
}

// Las mascotas saltan alternándose con cada toque aceptado.
function cheerMascot() {
  cheerMascot.turn = !cheerMascot.turn;
  const mascot = $(cheerMascot.turn ? 'mascotLeft' : 'mascotRight');
  mascot.classList.remove('cheer');
  void mascot.offsetWidth;
  mascot.classList.add('cheer');
}

// "+1" flotante sobre el botón.
function createTapEffect() {
  const button = $('tapButton');
  const rect = button.getBoundingClientRect();
  const effect = document.createElement('span');
  effect.className = 'tap-pop';
  effect.textContent = '+1';
  effect.style.left = `${rect.left + rect.width / 2 + (Math.random() * 40 - 20)}px`;
  effect.style.top = `${rect.top + 20}px`;
  document.body.appendChild(effect);
  setTimeout(() => effect.remove(), 600);
}

// ----------------------------------------------------
// CLUSTER: autoscaling simulado (solo visual)
// ----------------------------------------------------
//
// Reproduce el recorrido de una request tal como se explica en la charla:
//
//   Cliente (web del jugador)
//     └─ Kubernetes Cluster
//          Gateway (kgateway) + HTTPRoute
//            ├─ Service stable (90 %) → Pods v1
//            └─ Service canary (10 %) → Pods v2
//
// Un HPA simulado calcula las req/s y ajusta las réplicas de cada grupo
// según su parte del tráfico: sube rápido (una réplica cada 350 ms) y
// baja con enfriamiento, como el HPA real con su ventana de
// estabilización.
//
// Dos instancias:
//  - jugador: las req/s salen de sus propios toques (request()).
//  - host: las req/s vienen del servidor (setRemote()), sumando a toda la
//    sala; el objetivo por pod se adapta al número de jugadores para que
//    el cluster llegue a saturarse cuando todos tocan.

const GROUPS = {
  stable: { label: 'v1', weight: 0.9, maxPods: 9 },
  canary: { label: 'v2', weight: 0.1, maxPods: 3 }
};

// Nombres de pods con sabor local: <sustantivo>-<adjetivo>.
const POD_NOUNS = [
  'cuy', 'llama', 'alpaca', 'condor', 'puma', 'vicuna', 'pisco', 'ceviche',
  'chifa', 'causa', 'lomo', 'anticucho', 'picaron', 'chicha', 'inca', 'quinua',
  'choclo', 'papa', 'gopher', 'kubelet', 'machu', 'cusco', 'tuna', 'ajiseco'
];

const POD_ADJECTIVES = [
  'turbo', 'ninja', 'veloz', 'zen', 'rockero', 'feroz', 'magico', 'chevere',
  'bacan', 'picante', 'dormido', 'hambriento', 'saltarin', 'valiente', 'bailarin',
  'misterioso', 'chismoso', 'lento', 'brillante', 'travieso', 'sabroso', 'cosmico'
];

function clusterTemplate(opts) {
  const totalPods = GROUPS.stable.maxPods + GROUPS.canary.maxPods;

  return `
    <div class="cluster-head">
      <div class="cluster-title">
        <img src="/assets/k8s.png" alt="Kubernetes">
        <b>${opts.title}</b>
        <small class="hpa-status">HPA · esperando tráfico</small>
      </div>
      <div class="cluster-metrics">
        <div><span>REQ/S</span><b class="rps">0</b></div>
        <div><span>PODS</span><b><span class="replicas">2</span><small>/${totalPods}</small></b></div>
        <div><span>CPU</span><b class="cpu">0%</b></div>
      </div>
      <div class="cpu-bar"><i></i></div>
      <div class="cluster-events">
        <span class="node">node/kind-worker</span>
        <span class="k8s-event">Normal · Scheduled · tap-game listo para recibir tráfico</span>
      </div>
    </div>

    <div class="path">

      <div class="hop client">
        <span class="hop-kind">Cliente</span>
        <b>web del jugador</b>
        <small>${opts.remote ? 'todos los teléfonos de la sala' : 'tu teléfono'} · HTTPS</small>
      </div>

      <div class="link"><i></i></div>

      <div class="k8s-box">
        <span class="k8s-box-title">Kubernetes Cluster · kind</span>

        <div class="row">
          <div class="hop gateway">
            <span class="hop-kind">Gateway</span>
            <b>kgateway</b>
            <small>entrypoint · ${opts.host}</small>
          </div>
          <div class="hop route">
            <span class="hop-kind">HTTPRoute</span>
            <b>tap-game</b>
            <small>backends · 90 / 10</small>
          </div>
        </div>

        <div class="link"><i></i></div>

        <div class="row">
          <div class="hop service stable">
            <span class="hop-kind">Service · stable</span>
            <b>svc/tap-game</b>
            <small>weight 90%</small>
          </div>
          <div class="hop service canary">
            <span class="hop-kind">Service · canary</span>
            <b>svc/tap-game-v2</b>
            <small>weight 10%</small>
          </div>
        </div>

        <div class="link"><i></i></div>

        <div class="row">
          <div class="hop deployment stable">
            <span class="hop-kind">Pods v1</span>
            <div class="pods" data-group="stable" aria-live="polite"></div>
          </div>
          <div class="hop deployment canary">
            <span class="hop-kind">Pods v2</span>
            <div class="pods" data-group="canary" aria-live="polite"></div>
          </div>
        </div>
      </div>

    </div>
  `;
}

function createCluster(root, options) {
  const opts = Object.assign({
    remote: false,
    title: 'deploy/tap-game',
    host: 'game.f13.pe',
    targetRps: 2.5,      // req/s que "aguanta" un pod antes de escalar
    window: 2000,        // ventana para calcular req/s (modo local)
    scaleUpEvery: 350,
    scaleDownEvery: 1200,
    cooldown: 1500,      // sin tráfico durante este tiempo antes de bajar
    source: null         // () => elemento externo desde el que sale la request (botón)
  }, options);

  root.innerHTML = clusterTemplate(opts);

  const q = (selector) => root.querySelector(selector);
  const maxPods = GROUPS.stable.maxPods + GROUPS.canary.maxPods;

  const cluster = {
    root,
    opts,
    taps: [],
    groups: {},
    remoteRps: 0,
    lastTap: 0,
    timer: null,

    init() {
      this.reset();
      if (!this.timer) this.timer = setInterval(() => this.tick(), 250);
      return this;
    },

    reset() {
      this.taps = [];
      this.remoteRps = 0;
      this.lastTap = 0;

      for (const [name, def] of Object.entries(GROUPS)) {
        const el = q(`.pods[data-group="${name}"]`);
        el.innerHTML = '';
        this.groups[name] = { name, def, el, pods: [], lastScaleUp: 0, lastScaleDown: 0 };
        this.addPod(this.groups[name], true);
      }

      this.updateUI(0);
      this.event('Normal · Scheduled · tap-game listo para recibir tráfico');
    },

    // Nombre único entre los pods vivos de este cluster.
    podName() {
      const pick = (list) => list[Math.floor(Math.random() * list.length)];
      const alive = new Set(this.allPods().map(p => p.el.dataset.name));

      // Máximo 13 caracteres para que el nombre quepa entero en la tarjeta.
      for (let i = 0; i < 80; i++) {
        const name = `${pick(POD_NOUNS)}-${pick(POD_ADJECTIVES)}`;
        if (name.length <= 13 && !alive.has(name)) return name;
      }
      return `${pick(POD_NOUNS)}-${Math.floor(Math.random() * 90) + 10}`;
    },

    addPod(group, instant) {
      const el = document.createElement('div');
      el.className = `pod ${group.name}${instant ? '' : ' creating'}`;
      el.dataset.name = this.podName();
      el.innerHTML = `<img src="/assets/golang.png" alt=""><span class="pod-name">${el.dataset.name}</span>`;
      group.el.appendChild(el);

      const pod = { el, ready: !!instant };
      group.pods.push(pod);

      if (!instant) {
        setTimeout(() => {
          el.classList.remove('creating');
          pod.ready = true;
          this.event(`Normal · Started · pod/${el.dataset.name} (${group.def.label})`, 'up');
        }, 650);
      }

      return pod;
    },

    removePod(group) {
      const pod = group.pods.pop();
      if (!pod) return;
      pod.ready = false;
      pod.el.classList.add('terminating');
      setTimeout(() => pod.el.remove(), 450);
      this.event(`Normal · Killing · pod/${pod.el.dataset.name} (${group.def.label})`, 'down');
    },

    allPods() {
      return Object.values(this.groups).flatMap(g => g.pods);
    },

    // Modo local: un toque del jugador.
    request() {
      const now = Date.now();
      this.taps.push(now);
      this.lastTap = now;
      this.spawnRequest();
    },

    // Modo remoto: tráfico global que manda el servidor.
    setRemote(rps, playerCount) {
      this.remoteRps = rps;
      this.remoteAt = Date.now();
      if (rps > 0) this.lastTap = this.remoteAt;
      // Con todos tocando (~6 req/s por persona) el cluster llega al máximo.
      this.opts.targetRps = Math.max(3, Math.round((playerCount * 6) / maxPods));
    },

    // Partícula: (botón →) cliente → gateway → route → service → pod.
    // El HTTPRoute reparte 90 % a stable y 10 % a canary.
    spawnRequest() {
      if (document.hidden) return;

      const groupName = Math.random() < GROUPS.canary.weight ? 'canary' : 'stable';
      const group = this.groups[groupName];
      const ready = group.pods.filter(p => p.ready);
      const target = ready[Math.floor(Math.random() * ready.length)] || group.pods[0];
      if (!target) return;

      const center = (el) => {
        const r = el.getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      };

      const source = this.opts.source && this.opts.source();

      const hops = [];
      if (source) hops.push({ el: source, at: center(source) });
      hops.push({ el: q('.hop.client'), at: center(q('.hop.client')), ms: 140 });
      hops.push({ el: q('.hop.gateway'), at: center(q('.hop.gateway')), ms: 140 });
      hops.push({ el: q('.hop.route'), at: center(q('.hop.route')), ms: 120 });
      hops.push({ el: q(`.hop.service.${groupName}`), at: center(q(`.hop.service.${groupName}`)), ms: 140 });
      hops.push({ el: target.el, at: center(target.el), ms: 200, pod: true });

      const start = hops.shift();
      start.at.x += Math.random() * 24 - 12;

      const dot = document.createElement('span');
      dot.className = `request ${groupName}`;
      dot.style.left = `${start.at.x}px`;
      dot.style.top = `${start.at.y}px`;
      document.body.appendChild(dot);

      const flash = (el) => {
        el.classList.remove('hit');
        void el.offsetWidth;
        el.classList.add('hit');
      };

      let delay = 0;
      hops.forEach((hop, index) => {
        setTimeout(() => {
          dot.style.transitionDuration = `${hop.ms}ms`;
          dot.style.transform = `translate(${hop.at.x - start.at.x}px, ${hop.at.y - start.at.y}px) scale(${hop.pod ? .6 : 1})`;
          if (hop.pod) dot.style.opacity = '0.25';
        }, delay);
        delay += hop.ms;
        setTimeout(() => {
          flash(hop.el);
          if (index === hops.length - 1) dot.remove();
        }, delay);
      });
    },

    currentRps() {
      if (this.opts.remote) {
        // Si el servidor deja de mandar snapshots, el tráfico se considera cero.
        return Date.now() - (this.remoteAt || 0) > 2500 ? 0 : this.remoteRps;
      }
      const now = Date.now();
      this.taps = this.taps.filter(t => now - t < this.opts.window);
      return this.taps.length / (this.opts.window / 1000);
    },

    tick() {
      const now = Date.now();
      const rps = this.currentRps();

      // Cada grupo escala con su parte del tráfico.
      for (const group of Object.values(this.groups)) {
        const groupRps = rps * group.def.weight;
        const desired = Math.min(group.def.maxPods, Math.max(1, Math.ceil(groupRps / this.opts.targetRps)));
        const replicas = group.pods.length;

        if (desired > replicas && now - group.lastScaleUp > this.opts.scaleUpEvery) {
          group.lastScaleUp = now;
          this.addPod(group, false);
          this.event(`Normal · ScalingReplicaSet · tap-game-${group.def.label} scaled up to ${replicas + 1} (${groupRps.toFixed(1)} req/s)`, 'up');
        } else if (
          desired < replicas &&
          now - this.lastTap > this.opts.cooldown &&
          now - group.lastScaleDown > this.opts.scaleDownEvery
        ) {
          group.lastScaleDown = now;
          this.removePod(group);
          this.event(`Normal · ScalingReplicaSet · tap-game-${group.def.label} scaled down to ${replicas - 1}`, 'down');
        }
      }

      // En modo remoto las partículas representan una muestra del tráfico.
      if (this.opts.remote && rps > 0) {
        const n = Math.min(8, Math.ceil(rps / 4));
        for (let i = 0; i < n; i++) setTimeout(() => this.spawnRequest(), (250 / n) * i);
      }

      this.updateUI(rps);
    },

    updateUI(rps) {
      const replicas = this.allPods().filter(p => !p.el.classList.contains('terminating')).length || 1;
      const cpu = Math.min(100, Math.round((rps / replicas / this.opts.targetRps) * 100));

      q('.rps').textContent = rps >= 100 ? Math.round(rps) : rps.toFixed(1);
      q('.replicas').textContent = replicas;
      q('.cpu').textContent = `${cpu}%`;

      const bar = q('.cpu-bar i');
      bar.style.width = `${cpu}%`;
      bar.className = cpu >= 90 ? 'critical' : cpu >= 60 ? 'hot' : '';

      q('.hpa-status').textContent = rps === 0
        ? `HPA · ${replicas}/${maxPods} · sin tráfico`
        : `HPA · target ${this.opts.targetRps} req/s/pod · ${replicas}/${maxPods}`;

      root.classList.toggle('busy', rps > 0);
    },

    event(text, kind) {
      const el = q('.k8s-event');
      el.textContent = text;
      el.className = `k8s-event ${kind || ''}`;
    }
  };

  return cluster;
}

const playerCluster = createCluster($('arena'), { source: () => $('tapButton') });
const hostCluster = createCluster($('hostCluster'), { remote: true, title: 'cluster/kind-game · tráfico de la sala' });

// ----------------------------------------------------
// RENDER
// ----------------------------------------------------

function render(next) {
  state = next;
  const game = next.game || {};

  if (game.serverNow) serverOffset = game.serverNow - Date.now();

  // El epoch cambia con cada reset: nuestra sesión ya no existe.
  if (me && game.epoch && me.epoch !== game.epoch) {
    leaveGame('La partida se reinició. Vuelve a entrar.');
  }

  if ($('onlineCount')) $('onlineCount').textContent = next.onlineCount ?? 0;
  if ($('playerCount')) $('playerCount').textContent = next.playerCount ?? 0;

  renderLeaderboard(next.top || []);

  if (me && next.me) $('score').textContent = next.me.score;

  // Fuera de la partida no hay tráfico que mostrar, aunque la ventana de
  // req/s del servidor aún no se haya vaciado.
  if (hostMode()) {
    hostCluster.setRemote(game.status === 'running' ? (next.hitsPerSecond || 0) : 0, next.playerCount || 0);
  }

  updateGame(game);
  updateHost(game);
}

function renderLeaderboard(top) {
  const html = top.map((player, index) => `
    <div class="leader-row">
      <span class="rank">${index + 1}</span>
      <span class="mini-go"><i>GO</i></span>
      <span class="leader-name">${escapeHtml(player.name)}</span>
      <strong>${player.score}</strong>
    </div>
  `).join('');

  if ($('top')) $('top').innerHTML = html || '<div class="empty-board">Aún no hay jugadores</div>';
}

function updateGame(game) {
  const running = game.status === 'running';

  clearInterval(timerInterval);

  // Nueva ronda: los clusters vuelven al mínimo de réplicas.
  if (running && game.startedAt !== updateGame.lastStart) {
    updateGame.lastStart = game.startedAt;
    playerCluster.reset();
    hostCluster.reset();
  }

  if (running) {
    $('statusMessage').innerHTML = '<strong>¡SIGUE TOCANDO!</strong><small>Cada toque del rayo suma 1 punto.</small>';
    $('tapButton').disabled = !me;
    updateTimer(game.endsAt);
    timerInterval = setInterval(() => updateTimer(game.endsAt), 100);
  } else if (game.status === 'finished') {
    $('statusMessage').innerHTML = '<strong>¡TIEMPO!</strong><small>Revisa el TOP 5 en la pantalla principal.</small>';
    $('timer').textContent = '00:00';
    $('tapButton').disabled = true;
  } else {
    $('statusMessage').innerHTML = me
      ? '<strong>¡ESTÁS DENTRO!</strong><small>Espera el START del host.</small>'
      : '<strong>ESPERANDO AL HOST</strong><small>El juego comenzará para todos al mismo tiempo.</small>';
    $('timer').textContent = '—';
    $('tapButton').disabled = true;
  }
}

function updateTimer(endsAt) {
  const left = Math.max(0, endsAt - (Date.now() + serverOffset));
  const seconds = Math.ceil(left / 1000);
  $('timer').textContent = `00:${String(seconds).padStart(2, '0')}`;
  if (seconds <= 0) clearInterval(timerInterval);
}

function updateHost(game) {
  if (!$('hostStatus')) return;
  const labels = { lobby: 'LOBBY · ESPERANDO JUGADORES', running: '🟢 PARTIDA EN CURSO', finished: '🏆 PARTIDA TERMINADA' };
  $('hostStatus').textContent = labels[game.status] || 'LOBBY';
  $('startBtn').disabled = game.status === 'running';

  const left = game.status === 'running' ? Math.max(0, game.endsAt - (Date.now() + serverOffset)) : 0;
  $('hostTimer').textContent = game.status === 'running' ? `00:${String(Math.ceil(left / 1000)).padStart(2, '0')}` : '—';
}

// ----------------------------------------------------
// TIEMPO REAL
// ----------------------------------------------------

function connectEvents() {
  if (eventSource) eventSource.close();

  const url = me ? `/api/events?id=${encodeURIComponent(me.id)}` : '/api/events';
  eventSource = new EventSource(url);

  eventSource.onmessage = (event) => {
    try {
      render(JSON.parse(event.data));
    } catch (error) {
      console.error('Invalid event', error);
    }
  };

  eventSource.onerror = () => {
    // EventSource reconecta automáticamente.
  };
}

// ----------------------------------------------------
// MODO
// ----------------------------------------------------

function showMode() {
  const host = hostMode() !== null;

  $('joinScreen').classList.toggle('hidden', host || !!me);
  $('gameScreen').classList.toggle('hidden', host || !me);
  $('hostScreen').classList.toggle('hidden', !host);
  $('hostQr').classList.toggle('hidden', !host);

  if (host) {
    const playersUrl = `${location.origin}/`;
    $('gameUrl').textContent = playersUrl;
    $('qr').src = `/api/qr.png?url=${encodeURIComponent(playersUrl)}`;
  }
}

function toast(message) {
  const el = $('toast');
  el.textContent = message;
  el.classList.add('show');
  clearTimeout(toast.timeout);
  toast.timeout = setTimeout(() => el.classList.remove('show'), 1800);
}

function escapeHtml(value) {
  return String(value).replace(/[&<>'"]/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
  }[char]));
}

$('randomBtn').addEventListener('click', randomName);
$('joinBtn').addEventListener('click', join);
$('name').addEventListener('keydown', (event) => { if (event.key === 'Enter') join(); });
$('tapButton').addEventListener('pointerdown', (event) => {
  event.preventDefault();
  hit();
});
$('startBtn').addEventListener('click', startGame);
$('resetBtn').addEventListener('click', resetGame);

window.addEventListener('hashchange', showMode);

showMode();
playerCluster.init();
hostCluster.init();
// El timer del host se refresca desde el propio tick del reloj.
setInterval(() => { if (hostMode()) updateHost(state.game || {}); }, 250);
connectEvents();
restoreSession();
