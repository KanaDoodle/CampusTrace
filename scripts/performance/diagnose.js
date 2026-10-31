import http from 'k6/http';
import exec from 'k6/execution';
import { Counter, Trend } from 'k6/metrics';
import { validFixture } from './contract.mjs';
import { validDiagnosis } from './diagnose_contract.mjs';
const f = JSON.parse(open('/input/fixture.json'));
if (!validFixture(f, __ENV.PERF_RUN_ID) || !f.sparse || f.sparse.length !== 2) throw Error('Owned diagnostic fixture required');
const seconds = Number(__ENV.PERF_SECONDS), rate = Number(__ENV.PERF_RATE), mode = __ENV.PERF_MODE;
if (seconds < 5 || seconds > 120 || rate < 1 || rate > 100 || !['basic','fulltext'].includes(mode)) throw Error('Invalid bounded workload');
const attempts = new Counter('attempts'), failures = new Counter('failures');
const latency = new Trend('latency_ms', true);
const okStatus = new Counter('http_status_200'), busyStatus = new Counter('http_status_503'), otherStatus = new Counter('http_status_other');
const classes = ['common', 'sparse', 'miss'];
const trends = Object.fromEntries(classes.map(k => [k, new Trend(`${k}_ms`, true)]));
const counts = Object.fromEntries(classes.map(k => [k, new Counter(`${k}_attempts`)]));
const errors = Object.fromEntries(classes.map(k => [k, new Counter(`${k}_failures`)]));
export const options = {
 scenarios:{reader:{executor:'constant-arrival-rate', rate, timeUnit:'1s', duration:`${seconds}s`, preAllocatedVUs:16, maxVUs:16, gracefulStop:'5s'}},
 thresholds:{failures:['count==0'], dropped_iterations:['count==0'], attempts:[`count>=${rate*seconds}`]},
 systemTags:['status','method','name','scenario','expected_response'],
 summaryTrendStats:['avg','min','med','max','p(95)','p(99)'],
};
export default function() {
 const i = exec.scenario.iterationInTest;
 // 50% common, 25% sparse, 25% miss; each class covers both owners.
 const kind = ['common','common','sparse','miss'][i % 4], u = Math.floor(i / 4) % 2;
 const user = f.users[u], sparse = f.sparse[u];
 const text = kind === 'common' ? user.topics[0] : kind === 'sparse' ? sparse.text : 'absentdiagnosticterm';
 const r = http.post(`${f.base_url}/api/jobs/search`, JSON.stringify({text, company:f.company, limit:10}), {
  headers:{Authorization:`Bearer ${user.token}`, 'Content-Type':'application/json'}, timeout:'5s', redirects:0, tags:{name:`search ${kind}`},
 });
 let b; try { b = r.json(); } catch (_) { b = null; }
 const valid = r.status === 200 && validDiagnosis(b, user.id, f.company, mode, kind, sparse.id);
 okStatus.add(r.status === 200 ? 1 : 0); busyStatus.add(r.status === 503 ? 1 : 0); otherStatus.add([200,503].includes(r.status) ? 0 : 1);
 attempts.add(1); failures.add(valid?0:1); latency.add(r.timings.duration);
 trends[kind].add(r.timings.duration); counts[kind].add(1); errors[kind].add(valid?0:1);
}
export function handleSummary(data) {
 return {'/results/summary.json':JSON.stringify({mode,seconds,rate,metrics:data.metrics},null,2)};
}
