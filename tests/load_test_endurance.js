import http from 'k6/http';
import { check, group } from 'k6';
import { Rate, Counter } from 'k6/metrics';

const rateLimited  = new Rate('rate_limited');
const actualErrors = new Rate('actual_errors');
const requestCount = new Counter('request_count');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Sustained load for 10 minutes to surface memory leaks and latency drift.
export const options = {
  scenarios: {
    endurance: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 200,
      stages: [
        { duration: '10m', target: 50 },
        { duration: '1m',  target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    actual_errors:     ['rate<0.01'],
    rate_limited:      ['rate<0.50'],
  },
};

const HEADERS = { 'Content-Type': 'application/json' };

function randomSession() {
  return `session_${Math.floor(Math.random() * 100)}`;
}

function randomIP() {
  return `10.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 254) + 1}`;
}

let iteration = 0;

export default function () {
  iteration++;

  group('endurance check', () => {
    const body = JSON.stringify({
      client_id: 'application',
      route: '/api/videos',
      method: 'GET',
      session_id: randomSession(),
      ip: randomIP(),
    });
    const res = http.post(`${BASE_URL}/check`, body, { headers: HEADERS });
    check(res, {
      'status 200 or 429': (r) => r.status === 200 || r.status === 429,
    });
    rateLimited.add(res.status === 429);
    actualErrors.add(res.status >= 500 || res.status === 0);
    requestCount.add(1);
  });

  // Rotate through other endpoints every 20 iterations to exercise more code paths.
  if (iteration % 20 === 0) {
    group('policies inspection', () => {
      const client = Math.random() < 0.5 ? 'application' : 'partner';
      const res = http.get(`${BASE_URL}/policies/${client}`);
      check(res, { 'policies 200': (r) => r.status === 200 });
      requestCount.add(1);
    });
  }

  if (iteration % 50 === 0) {
    group('all clients', () => {
      const res = http.get(`${BASE_URL}/policies`);
      check(res, { 'clients 200': (r) => r.status === 200 });
      requestCount.add(1);
    });
  }

  if (iteration % 100 === 0) {
    group('health check', () => {
      const res = http.get(`${BASE_URL}/health`);
      check(res, { 'health 200': (r) => r.status === 200 });
      requestCount.add(1);
    });
  }
}
