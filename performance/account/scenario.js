import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';

http.setResponseCallback(http.expectedStatuses(200));
const budgetSkipped = new Counter('budget_skipped_iterations');

// Use run.py: this file is deliberately not a configurable public CLI.
const c = JSON.parse(__ENV.ACCOUNT_CONFIG);
if (!/^http:\/\/127\.0\.0\.1:[0-9]{4,5}$/.test(c.target) ||
    !Number.isInteger(c.rate) || c.rate < 1 || c.rate > 5 ||
    !Number.isInteger(c.duration) || c.duration < 1 || c.duration > 60 ||
    !['normal', 'mixed-4xx'].includes(c.profile)) {
  throw new Error('invalid guarded input');
}
export const options = {
  scenarios: { account: {
    executor: 'constant-arrival-rate', rate: c.rate, timeUnit: '1s',
    duration: `${c.duration}s`, preAllocatedVUs: 5, maxVUs: 5, gracefulStop: '3s',
  } },
  // A reused HTTP/1.1 connection can trigger an invisible Go transport GET retry.
  // Fresh connections keep server receives within the iteration request budget.
  noConnectionReuse: true,
  maxRedirects: 0, batch: 1, batchPerHost: 1, discardResponseBodies: true,
  insecureSkipTLSVerify: false, throw: false,
  tags: { test_label: c.label, target_revision: c.revision, profile: c.profile },
  // Scenario correctness only; no SLO or performance gate. 400 remains http_req_failed.
  thresholds: { checks: ['rate==1'], dropped_iterations: ['count==0'] },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

export default function () {
  const n = exec.scenario.iterationInTest;
  // k6 can start an extra iteration on the duration boundary. Never send an extra HTTP.
  if (n >= c.max_http_requests) {
    budgetSkipped.add(1);
    return;
  }
  budgetSkipped.add(0);
  const invalid = c.profile === 'mixed-4xx' && n % 2 === 1;
  const path = invalid ? '/api/accounts/0' : '/api/accounts';
  const response = http.get(c.target + path, {
    timeout: '2s', redirects: 0, tags: { name: invalid ? 'account-invalid-id' : 'account-list' },
  });
  check(response, { 'expected Account status': r => r.status === (invalid ? 400 : 200) });
}

export function handleSummary(data) {
  return { [__ENV.ACCOUNT_SUMMARY]: JSON.stringify({ input: c, finished_at_utc: new Date().toISOString(), ...data }, null, 2) + '\n' };
}
