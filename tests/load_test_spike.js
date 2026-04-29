import http from 'k6/http';
import { check, group } from 'k6';
import { Rate } from 'k6/metrics';

const rateLimited  = new Rate('rate_limited');
const actualErrors = new Rate('actual_errors');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Normal load → sudden spike × 10 → recovery → ramp down (~30s total).
export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 1000,
      stages: [
        { duration: '10s', target: 50 },   // normal baseline
        { duration: '5s',  target: 500 },  // spike
        { duration: '10s', target: 50 },   // recovery
        { duration: '5s',  target: 0 },    // ramp down
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(99)<2000'],
    actual_errors:     ['rate<0.01'],
    rate_limited:      ['rate<0.80'],
  },
};

const HEADERS = { 'Content-Type': 'application/json' };

function randomSession() {
  return `session_${Math.floor(Math.random() * 200)}`;
}

function randomIP() {
  return `10.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 254) + 1}`;
}

export default function () {
  group('spike check', () => {
    const body = JSON.stringify({
      client_id: 'application',
      route: '/api/videos',
      method: 'GET',
      session_id: randomSession(),
      ip: randomIP(),
    });
    const res = http.post(`${BASE_URL}/check`, body, { headers: HEADERS });
    check(res, {
      'not a server error': (r) => r.status < 500,
    });
    rateLimited.add(res.status === 429);
    actualErrors.add(res.status >= 500 || res.status === 0);
  });

  if (Math.random() < 0.05) {
    group('health during spike', () => {
      const res = http.get(`${BASE_URL}/health`);
      check(res, { 'health ok': (r) => r.status === 200 });
    });
  }
}
