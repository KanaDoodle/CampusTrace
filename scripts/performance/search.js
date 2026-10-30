import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter, Trend } from 'k6/metrics';
import { validSearch, validFixture } from './contract.mjs';

const fixture = JSON.parse(open('/input/fixture.json'));
if (!validFixture(fixture, __ENV.PERF_RUN_ID)) throw Error('Only the owned synthetic lab target is allowed');
const phase = __ENV.PERF_CASE;
if (!['fulltext', 'fallback', 'rebuild', 'mixed'].includes(phase)) throw Error('Invalid phase');
const seconds = Number(__ENV.PERF_SECONDS), rate = Number(__ENV.PERF_RATE), writeRate = Number(__ENV.PERF_WRITE_RATE), maxVUs = Number(__ENV.PERF_MAX_VUS);
if (seconds < 10 || seconds > 120 || rate < 1 || rate > 100 || maxVUs < 4 || maxVUs > 64 || writeRate < 1 || writeRate > 5) throw Error('Invalid bounded workload');
const queries = new Counter('query_requests');
const queryFailures = new Counter('query_failures');
const basic = new Counter('mode_basic');
const fulltext = new Counter('mode_fulltext');
const queryLatency = new Trend('query_ms', true);
const basicLatency = new Trend('query_basic_ms', true);
const fulltextLatency = new Trend('query_fulltext_ms', true);
const writes = new Counter('write_requests');
const writeFailures = new Counter('write_failures');
const minimum = Math.floor(rate * seconds * 0.9);
const thresholds = {
  checks: ['rate==1'], http_req_failed: ['rate==0'], dropped_iterations: ['count==0'],
  query_failures: ['count==0'], query_requests: [`count>=${minimum}`],
};
if (phase === 'fulltext') { thresholds.mode_fulltext = [`count>=${minimum}`]; thresholds.mode_basic = ['count==0']; }
if (phase === 'fallback') { thresholds.mode_basic = [`count>=${minimum}`]; thresholds.mode_fulltext = ['count==0']; }
if (phase === 'rebuild') thresholds.mode_basic = ['count>0'];
if (phase === 'mixed') { thresholds.write_requests = [`count>=${Math.floor(writeRate * seconds * 0.9)}`]; thresholds.write_failures = ['count==0']; }
const reader = {executor:'constant-arrival-rate', exec:'query', rate, timeUnit:'1s', duration:`${seconds}s`, preAllocatedVUs:maxVUs, maxVUs, gracefulStop:'10s'};
export const options = {
  scenarios: phase === 'mixed' ? {reader, writer:{executor:'constant-arrival-rate', exec:'update', rate:writeRate, timeUnit:'1s', duration:`${seconds}s`, preAllocatedVUs:1, maxVUs:4, gracefulStop:'10s'}} : {reader},
  thresholds, systemTags:['status','method','name','scenario','expected_response'],
  summaryTrendStats:['avg','min','med','max','p(95)','p(99)'],
};
export function setup() { console.info('PERF_LOAD_STARTED'); }
function params(user, name) { return {headers:{Authorization:`Bearer ${user.token}`, 'Content-Type':'application/json'}, timeout:'5s', redirects:0, tags:{name}}; }
export function query() {
  const iteration = exec.scenario.iterationInTest;
  const user = fixture.users[iteration % fixture.users.length];
  const topic = user.topics[Math.floor(iteration / fixture.users.length) % user.topics.length];
  const response = http.post(`${fixture.base_url}/api/jobs/search`, JSON.stringify({text:topic,company:fixture.company,limit:10}), params(user,'POST /api/jobs/search'));
  queries.add(1); queryLatency.add(response.timings.duration); basic.add(0); fulltext.add(0);
  let body; try { body = response.json(); } catch (_) { body = null; }
  const valid = response.status === 200 && validSearch(body, user.id, fixture.company, 10);
  queryFailures.add(valid ? 0 : 1); check(valid, {'search response and account isolation': value => value});
  if (body?.mode === 'basic') { basic.add(1); basicLatency.add(response.timings.duration); }
  if (body?.mode === 'fulltext') { fulltext.add(1); fulltextLatency.add(response.timings.duration); }
}
export function update() {
  const marker = `perfwrite${String(exec.scenario.iterationInTest).padStart(8,'0')}`;
  const payload = {...fixture.writer.input,text:fixture.writer.input.text+' '+marker};
  const response = http.post(`${fixture.base_url}/api/ingest`, JSON.stringify(payload), params(fixture.users[0],'POST /api/ingest'));
  writes.add(1); let body; try { body = response.json(); } catch (_) { body = null; }
  const valid = response.status === 200 && body?.job_id === fixture.writer.job_id;
  writeFailures.add(valid ? 0 : 1); check(valid, {'update preserves canonical job': value => value});
}
export function handleSummary(data) {
  return {'/results/summary.json':JSON.stringify({schema_version:1,phase,metrics:data.metrics,duration_ms:data.state?.testRunDurationMs},null,2), stdout:`PERF_LOAD_FINISHED ${phase}\n`};
}
