import http from 'k6/http';
import { check, sleep } from 'k6';

// k6 run load-test.js
// k6 run -e BASE=http://192.168.1.10:8080 -e HOST_TOKEN=secreto load-test.js

const BASE = __ENV.BASE || 'http://localhost:8080';
const HOST_TOKEN = __ENV.HOST_TOKEN || '';

const hostHeaders = HOST_TOKEN ? { 'X-Host-Token': HOST_TOKEN } : {};

export const options = {
  scenarios: {
    // 500 jugadores se unen en el lobby y tocan durante ~30 s.
    players: {
      executor: 'per-vu-iterations',
      vus: 500,
      iterations: 1,
      maxDuration: '45s',
    },
    // El host pulsa START a los 3 s, cuando todos ya están dentro
    // (nadie puede unirse con la partida en curso).
    host: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      startTime: '3s',
      exec: 'host',
    },
  },
  thresholds: {
    'checks{name:join}': ['rate>0.99'],
    'checks{name:score}': ['rate>0.99'],
    http_req_duration: ['p(95)<200'],
  },
};

// Arranca desde cero.
export function setup() {
  const reset = http.post(`${BASE}/api/reset`, null, { headers: hostHeaders });

  if (reset.status !== 200) {
    throw new Error(`no se pudo resetear la partida (${reset.status}); ¿falta HOST_TOKEN?`);
  }
}

export function host() {
  const start = http.post(`${BASE}/api/start`, null, { headers: hostHeaders });
  check(start, { 'start OK': (r) => r.status === 200 }, { name: 'start' });
}

export default function () {
  // Nombre único por VU e iteración para que nunca choque con un 409.
  const join = http.post(
    `${BASE}/api/join`,
    JSON.stringify({ name: `LT-${__VU}-${__ITER}` }),
    { headers: { 'Content-Type': 'application/json' }, tags: { name: 'join' } }
  );

  if (!check(join, { 'join OK': (r) => r.status === 200 }, { name: 'join' })) return;

  const player = join.json();

  // Aproximadamente 10 toques por segundo; los primeros llegan antes del
  // START y el servidor los ignora (accepted=false, pero 200).
  for (let i = 0; i < 330; i++) {
    const score = http.post(`${BASE}/api/score?id=${player.id}`, null, { tags: { name: 'score' } });
    check(score, { 'score OK': (r) => r.status === 200 }, { name: 'score' });
    sleep(0.1);
  }
}
