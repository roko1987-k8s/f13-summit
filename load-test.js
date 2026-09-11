import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE = 'http://localhost:8080';

export const options = {
  scenarios: {
    players: {
      executor: 'constant-vus',
      vus: 500,
      duration: '30s',
    },
  },
};

export default function () {
  // Cada usuario se registra
  const join = http.post(
    `${BASE}/api/join`,
    JSON.stringify({
      name: `LoadTest-${__VU}`,
    }),
    {
      headers: {
        'Content-Type': 'application/json',
      },
    }
  );

  check(join, {
    'join OK': (r) => r.status === 200,
  });

  const player = join.json();

  // Simular aproximadamente 10 toques por segundo
  for (let i = 0; i < 300; i++) {
    http.post(`${BASE}/api/score?id=${player.id}`);
    sleep(0.1);
  }
}
