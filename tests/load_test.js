import http from 'k6/http';
import { check, group } from 'k6';
import { Rate } from 'k6/metrics';

const rateLimited  = new Rate('rate_limited');
const actualErrors = new Rate('actual_errors'); // 5xx / network errors only

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Ramp up to 100 req/s over 30s, sustain for 1min, ramp down over 10s.
export const options = {
  scenarios: {
    load: {
      executor: 'ramping-arrival-rate',
      startRate: 10,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 300,
      stages: [
        { duration: '30s', target: 100 },
        { duration: '60s', target: 100 },
        { duration: '10s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    actual_errors:     ['rate<0.01'],  // real failures (5xx, timeout) < 1%
    rate_limited:      ['rate<0.50'],  // 429s are expected, not failures
  },
};

const HEADERS = { 'Content-Type': 'application/json' };

// 100 sessions × 100 req/min limit = headroom for the load test.
function randomSession() {
  return `session_${Math.floor(Math.random() * 100)}`;
}

function randomIP() {
  return `10.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 254) + 1}`;
}

const CLIENTS = ['partner', 'application', 'internal_service', 'api_consumer'];

export default function () {
  // partner — high volume, global quota on videos
  group('partner — videos', () => {
    const res = http.post(`${BASE_URL}/check`, JSON.stringify({
      client_id: 'partner', route: '/api/videos', method: 'GET',
    }), { headers: HEADERS });
    check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
    rateLimited.add(res.status === 429);
    actualErrors.add(res.status >= 500 || res.status === 0);
  });

  // application — per-session quota on videos (most common app traffic)
  if (Math.random() < 0.60) {
    group('application — videos', () => {
      const res = http.post(`${BASE_URL}/check`, JSON.stringify({
        client_id: 'application', route: '/api/videos', method: 'GET',
        session_id: randomSession(), ip: randomIP(),
      }), { headers: HEADERS });
      check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
      rateLimited.add(res.status === 429);
    });
  }

  // application — low-limit users route, exercises quota exhaustion
  if (Math.random() < 0.15) {
    group('application — users', () => {
      const res = http.post(`${BASE_URL}/check`, JSON.stringify({
        client_id: 'application', route: '/api/users', method: 'GET',
      }), { headers: HEADERS });
      check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
      rateLimited.add(res.status === 429);
    });
  }

  // application — payment, very low limit per IP
  if (Math.random() < 0.05) {
    group('application — payment', () => {
      const res = http.post(`${BASE_URL}/check`, JSON.stringify({
        client_id: 'application', route: '/api/payment', method: 'POST',
        ip: randomIP(),
      }), { headers: HEADERS });
      check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
      rateLimited.add(res.status === 429);
    });
  }

  // internal_service — quasi-unlimited, simulates internal microservice calls
  if (Math.random() < 0.10) {
    group('internal_service', () => {
      const res = http.post(`${BASE_URL}/check`, JSON.stringify({
        client_id: 'internal_service', route: '/internal/jobs', method: 'POST',
      }), { headers: HEADERS });
      check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
      rateLimited.add(res.status === 429);
    });
  }

  // api_consumer — per-IP quota
  if (Math.random() < 0.10) {
    group('api_consumer', () => {
      const res = http.post(`${BASE_URL}/check`, JSON.stringify({
        client_id: 'api_consumer', route: '/api/videos', method: 'GET',
        ip: randomIP(),
      }), { headers: HEADERS });
      check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
      rateLimited.add(res.status === 429);
    });
  }

  // Policy inspection across all client types (5% of requests).
  if (Math.random() < 0.05) {
    group('policies', () => {
      const client = CLIENTS[Math.floor(Math.random() * CLIENTS.length)];
      const res = http.get(`${BASE_URL}/policies/${client}`);
      check(res, { 'policies 200': (r) => r.status === 200 });
    });
  }

  // Health monitoring (2% of requests).
  if (Math.random() < 0.02) {
    group('health', () => {
      const res = http.get(`${BASE_URL}/health`);
      check(res, { 'health 200': (r) => r.status === 200 });
    });
  }
}
