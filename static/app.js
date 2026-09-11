let me = null;
let state = { game: { status: 'lobby', endsAt: 0 }, players: [], top: [] };
let timerInterval = null;
let eventSource = null;

const $ = (id) => document.getElementById(id);

const randomNames = [
  'PixelGopher', 'KubeNinja', 'GoRunner', 'PodMaster', 'CloudFox',
  'ByteBear', 'KindHero', 'GopherAce', 'NodeWizard', 'ClusterKid',
  'GoPilot', 'TinyPod', 'KubeRider', 'PixelBot', 'CloudKid'
];

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

    if (!response.ok) throw new Error('join failed');

    me = await response.json();
    localStorage.setItem('kindGamePlayerId', me.id);
    localStorage.setItem('kindGamePlayerName', me.name);

    $('playerName').textContent = me.name;
    $('score').textContent = '0';
    $('joinScreen').classList.add('hidden');
    $('gameScreen').classList.remove('hidden');

    $('statusMessage').innerHTML = '<strong>¡ESTÁS DENTRO!</strong><small>Espera el START del host.</small>';
    toast(`¡Bienvenido, ${me.name}!`);
  } catch (error) {
    toast('No pudimos registrarte. Intenta nuevamente.');
  }
}

async function startGame() {
  const response = await fetch('/api/start', { method: 'POST' });
  if (!response.ok) toast('No se pudo iniciar la partida.');
}

async function resetGame() {
  await fetch('/api/reset', { method: 'POST' });
}

async function hit() {
  if (!me || state.game.status !== 'running') return;

  const response = await fetch(`/api/score?id=${encodeURIComponent(me.id)}`, {
    method: 'POST'
  });

  if (!response.ok) return;

  const data = await response.json();

  if (data.accepted) {
    $('score').textContent = data.score;
    animateGopher();
    createTapEffect();
    if (navigator.vibrate) navigator.vibrate(8);
  }
}

function animateGopher() {
  const gopher = $('gopher');
  const direction = Math.random() > 0.5 ? 1 : -1;
  const distance = 20 + Math.random() * 55;

  gopher.classList.remove('spin', 'dash');
  void gopher.offsetWidth;

  if (Math.random() > 0.35) {
    gopher.style.setProperty('--jump-x', `${direction * distance}px`);
    gopher.classList.add('dash');
  } else {
    gopher.classList.add('spin');
  }
}

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

function render(next) {
  state = next;
  const players = next.players || [];
  const top = next.top || [];

  if ($('onlineCount')) $('onlineCount').textContent = players.filter(p => p.online).length;

  renderLeaderboard(top);

  if (me) {
    const current = players.find(p => p.id === me.id);
    if (current) $('score').textContent = current.score;
  }

  updateGame(next.game || {});
  updateHost(next.game || {});
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
  const left = Math.max(0, endsAt - Date.now());
  const seconds = Math.ceil(left / 1000);
  $('timer').textContent = `00:${String(seconds).padStart(2, '0')}`;
  if (seconds <= 0) clearInterval(timerInterval);
}

function updateHost(game) {
  if (!$('hostStatus')) return;
  const labels = { lobby: 'LOBBY · ESPERANDO JUGADORES', running: '🟢 PARTIDA EN CURSO', finished: '🏆 PARTIDA TERMINADA' };
  $('hostStatus').textContent = labels[game.status] || 'LOBBY';
  $('startBtn').disabled = game.status === 'running';
}

function connectEvents() {
  eventSource = new EventSource('/api/events');

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

function showMode() {
  const host = location.hash.toLowerCase() === '#host';

  $('joinScreen').classList.toggle('hidden', host);
  $('gameScreen').classList.toggle('hidden', host);
  $('hostScreen').classList.toggle('hidden', !host);

  if (host) {
    $('gameUrl').textContent = `${location.origin}/`;
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
$('tapButton').addEventListener('pointerdown', (event) => {
  event.preventDefault();
  hit();
});
$('startBtn').addEventListener('click', startGame);
$('resetBtn').addEventListener('click', resetGame);

window.addEventListener('hashchange', showMode);

showMode();
connectEvents();
fetch('/api/state').then(response => response.json()).then(render).catch(() => {});
