import { sleep } from 'k6';
import execution from 'k6/execution';
import http from 'k6/http';
import { randomBytes } from 'k6/crypto';
import { Counter, Rate, Trend } from 'k6/metrics';

// Each wave is [seconds since workload start, target pattern, number of jobs].
const profiles = {
  demo: {
    phases: [12, 25, 58, 75],
    waves: [
      [4, 'same', 3], [15, 'different', 4],
      [26, 'same', 10], [47, 'different', 10],
      [63, 'same', 3], [69, 'different', 4],
    ],
  },
  stress: {
    phases: [60, 120, 240, 300],
    waves: [
      [8, 'same', 4], [26, 'different', 4], [44, 'same', 4],
      [63, 'different', 6], [82, 'same', 8], [101, 'different', 8],
      [122, 'same', 14], [151, 'different', 14],
      [179, 'same', 14], [209, 'different', 14],
      [243, 'same', 4], [263, 'different', 4], [281, 'same', 4],
    ],
  },
  verify: {
    phases: [3, 5, 16, 22],
    waves: [[1, 'same', 3], [9, 'different', 4]],
  },
};

const profileName = __ENV.PROFILE || 'demo';
const profile = profiles[profileName];
if (!profile) throw new Error('PROFILE must be demo, stress or verify');

const baseURL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const [baselineEnd, rampEnd, peakEnd, workloadEnd] = profile.phases;
const expectedJobs = profile.waves.reduce((total, [, , count]) => total + count, 0);
const jobDeadlineMs = 60_000;
const pollIntervalMs = 1_000;
const setupTags = { traffic: 'setup' };
const ok = http.expectedStatuses(200);
const created = http.expectedStatuses(201);

const jobs = new Counter('migration_jobs');
const completionDuration = new Trend('migration_completion_duration', true);
const executionFailureRate = new Rate('migration_job_failure');

export const options = {
  discardResponseBodies: true,
  systemTags: ['name', 'method', 'status', 'expected_response', 'scenario'],
  summaryTrendStats: ['med', 'p(95)', 'max'],
  setupTimeout: '3m',
  scenarios: {
    requester_reads: {
      executor: 'constant-vus', exec: 'readAPI', env: { ACTOR: 'requester' },
      vus: 1, duration: `${workloadEnd}s`, gracefulStop: '6s',
    },
    reader_reads: {
      executor: 'ramping-vus', exec: 'readAPI', env: { ACTOR: 'reader' },
      startTime: `${baselineEnd}s`, startVUs: 0, gracefulStop: '6s', gracefulRampDown: '6s',
      stages: [
        { duration: `${rampEnd - baselineEnd}s`, target: 1 },
        { duration: `${peakEnd - rampEnd}s`, target: 1 },
        { duration: `${workloadEnd - peakEnd}s`, target: 0 },
      ],
    },
    migrations: {
      executor: 'per-vu-iterations', exec: 'runMigrations',
      vus: 1, iterations: 1,
      // Covers the last wave, its observation window and bounded HTTP calls.
      maxDuration: `${workloadEnd + 90}s`, gracefulStop: '6s',
    },
  },
  thresholds: {
    'http_req_failed{traffic:workload}': ['rate<0.01'],
    'http_req_duration{traffic:workload}': ['p(95)<1000'],
    'http_reqs{traffic:workload}': ['count>0'],
    migration_completion_duration: ['p(95)<35000'],
    migration_job_failure: ['rate<0.01'],
    'migration_jobs{outcome:queued}': [`count==${expectedJobs}`],
    'migration_jobs{finalized:true}': [`count==${expectedJobs}`],
    'migration_jobs{outcome:succeeded}': ['count>0'],
    'migration_jobs{outcome:failed}': ['count>=0'],
    'migration_jobs{outcome:unfinished}': ['count==0'],
    'migration_jobs{outcome:approval_error}': ['count==0'],
  },
};

function request(actor, method, path, name, tags, body, responseType = 'text') {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method === 'POST') headers.Origin = baseURL;
  if (actor && method === 'POST') {
    headers['X-CSRF-Token'] = actor.csrf;
  }

  // Forward the Secure cookie explicitly for the existing local HTTP backend.
  // Each actor follows its own rotating cookie and matching CSRF token.
  const cookies = actor ? { ssid: { value: actor.cookie, replace: true } } : {};
  const response = http.request(method, baseURL + path, body === undefined ? null : JSON.stringify(body), {
    headers, cookies, redirects: 0, responseType, timeout: '5s', tags: { ...tags, name },
    responseCallback: name === 'create_migration' || name === 'submit_review' ? created : ok,
  });
  const rotated = response.cookies.ssid?.[0]?.value;
  if (actor && rotated && rotated !== actor.cookie) {
    actor.cookie = rotated;
    actor.csrf = '';
  }
  return response;
}

function json(response) {
  try {
    return response.json();
  } catch {
    return null;
  }
}

function prepared(response, status, operation) {
  if (response.status !== status) {
    const code = json(response)?.error?.code;
    throw new Error(`Setup ${operation} failed (HTTP ${response.status}${code ? `, ${code}` : ''})`);
  }
  return json(response);
}

function currentSession(actor, tags) {
  const response = request(actor, 'GET', '/session-manager/me', 'session', tags);
  const me = response.status === 200 ? json(response) : null;
  if (me) actor.csrf = response.headers['X-Csrf-Token'] || '';
  return me;
}

function signIn(role, defaultUsername) {
  const username = __ENV[`${role}_USER`] || defaultUsername;
  const password = __ENV[`${role}_PASSWORD`] || __ENV.PASSWORD;
  if (!password) throw new Error(`Set ${role}_PASSWORD or PASSWORD for the disposable accounts`);

  http.cookieJar().clear(baseURL);
  const login = request(null, 'POST', '/session-manager/login', 'login', setupTags, { username, password });
  const actor = { cookie: login.cookies.ssid?.[0]?.value, csrf: '' };
  if (login.status !== 200 || !actor.cookie) throw new Error(`Setup login failed for ${role} (HTTP ${login.status})`);
  const me = currentSession(actor, setupTags);
  if (me?.username !== username || !actor.csrf) throw new Error(`Setup session check failed for ${role}`);
  return { ...actor, id: me.id };
}

const databaseList = '/api/accessible-databases';

export function setup() {
  const usernames = [__ENV.REQUESTER_USER || 'alice', __ENV.READER_USER || 'carol', __ENV.REVIEWER_USER || 'bob'];
  if (new Set(usernames).size !== 3) throw new Error('Use three distinct disposable accounts');
  const requester = signIn('REQUESTER', 'alice');
  const reader = signIn('READER', 'carol');
  const reviewer = signIn('REVIEWER', 'bob');

  const names = (__ENV.DATABASES || 'sales_db,academic_db,inventory_db').split(',').map(name => name.trim());
  if (names.length !== 3 || names.includes('') || new Set(names).size !== 3) {
    throw new Error('DATABASES must list three distinct disposable registrations');
  }
  const listings = {};
  for (const [role, actor] of Object.entries({ requester, reader, reviewer })) {
    const list = prepared(request(actor, 'GET', databaseList, 'list_databases', setupTags), 200, 'list databases');
    listings[role] = list.databases;
  }
  const targets = names.map(name => {
    const matches = listings.requester.filter(db => db.name === name);
    if (matches.length !== 1 || matches[0].permission !== 'ViewEdit') {
      throw new Error(`Requester needs one editable registration named ${name}`);
    }
    const { id } = matches[0];
    if (!listings.reviewer.some(db => db.id === id)) throw new Error(`Reviewer needs view access to ${name}`);
    return { path: `/api/databases/${id}`, sharedWithReader: listings.reader.some(db => db.id === id) };
  });
  if (!targets.some(target => target.sharedWithReader)) throw new Error('Reader needs view access to a test target');
  for (const target of targets) {
    prepared(request(requester, 'GET', target.path + '/schemas', 'list_schemas', setupTags), 200, 'connect to target');
  }

  const suffix = Array.from(new Uint8Array(randomBytes(6)), byte => byte.toString(16).padStart(2, '0')).join('');
  const run = `k6-${Date.now()}-${suffix}`;
  const waves = [];
  let number = 0;
  for (const [at, cohort, count] of profile.waves) {
    const fixtures = [];
    for (let i = 0; i < count; i++) {
      const { path } = targets[cohort === 'same' ? 0 : 1 + i % 2];
      if (!requester.csrf) currentSession(requester, setupTags);
      const migration = prepared(request(requester, 'POST', path + '/migrations', 'create_migration', setupTags, {
        name: `${run}-${++number}`,
        description: 'Populate and index scratch data for the k6 workload.',
        sql_script: `CREATE TEMP TABLE k6_backfill (id bigint PRIMARY KEY, value text NOT NULL) ON COMMIT DROP;
          INSERT INTO pg_temp.k6_backfill SELECT id, md5(id::text) FROM generate_series(1, 250000) AS id;
          CREATE INDEX ON pg_temp.k6_backfill (value);`,
        irreversible: true,
        irreversible_reason: 'The transaction drops its temporary data at commit.',
      }), 201, 'create migration');
      if (!requester.csrf) currentSession(requester, setupTags);
      const submitted = prepared(request(requester, 'POST', path + '/change-requests', 'submit_review', setupTags, {
        migration_id: migration.id, operation: 'apply',
      }), 201, 'submit review');
      const review = submitted.change_request;
      if (!submitted.job_id || submitted.job_id !== review.id || review.status !== 'pending_review' ||
          !review.approvals.length || review.approvals.some(approval => approval.required_leader_id !== reviewer.id)) {
        throw new Error('Every policy group on the test targets must have the configured reviewer as its active leader');
      }
      fixtures.push({ path, id: submitted.job_id });
    }
    waves.push({ at, cohort, fixtures });
  }
  console.log(`Prepared ${expectedJobs} pending reviews for ${profileName} (${run})`);
  return { requester, reader, reviewer, targets, waves };
}

function phaseAt(seconds) {
  if (seconds < baselineEnd) return 'baseline';
  if (seconds < rampEnd) return 'ramp';
  if (seconds < peakEnd) return 'peak';
  return seconds < workloadEnd ? 'recovery' : 'drain';
}

export function readAPI(data) {
  const actor = data[__ENV.ACTOR];
  const secondReader = __ENV.ACTOR === 'reader';
  const targets = secondReader ? data.targets.filter(target => target.sharedWithReader) : data.targets;
  const { path } = targets[execution.scenario.iterationInTest % targets.length];
  const elapsed = (Date.now() - execution.scenario.startTime) / 1000;
  const tags = { traffic: 'workload', phase: phaseAt(elapsed + (secondReader ? baselineEnd : 0)) };

  request(actor, 'GET', databaseList, 'list_databases', tags, undefined, 'none');
  request(actor, 'GET', path + '/schemas/public/tables', 'list_tables', tags, undefined, 'none');
  request(actor, 'GET', path + '/migrations', 'list_migrations', tags, undefined, 'none');
  request(actor, 'GET', path + '/migration-history?hide_rollback=true', 'execution_history', tags, undefined, 'none');
  sleep({ baseline: 0.9, ramp: 0.35, peak: 0.12, recovery: 0.9, drain: 0.9 }[tags.phase]);
}

export function runMigrations({ reviewer, waves }) {
  const started = execution.scenario.startTime;
  const approvalCutoff = started + workloadEnd * 1000;
  const pending = new Set();
  const record = (outcome, tags, count = 1) => {
    const finalized = outcome === 'succeeded' || outcome === 'failed';
    jobs.add(count, { ...tags, outcome, finalized: String(finalized) });
  };
  for (const outcome of ['queued', 'succeeded', 'failed', 'unfinished', 'approval_error']) {
    record(outcome, { traffic: 'workload' }, 0);
  }

  function markQueued(job) {
    if (job.queued) return;
    job.queued = true;
    record('queued', job.tags);
  }

  let nextWave = 0;
  while (nextWave < waves.length || pending.size > 0) {
    const wave = waves[nextWave];
    if (wave && Date.now() >= started + wave.at * 1000) {
      const tags = { traffic: 'workload', phase: phaseAt(wave.at), cohort: wave.cohort };
      for (const fixture of wave.fixtures) {
        // A refresh can rotate the cookie, requiring a second /me for its CSRF.
        for (let refresh = 0; refresh < 2 && !reviewer.csrf && Date.now() < approvalCutoff; refresh++) {
          currentSession(reviewer, tags);
        }
        const approvalStartedAt = Date.now();
        if (!reviewer.csrf || approvalStartedAt >= approvalCutoff) {
          record('approval_error', tags);
          console.warn(`Approval not sent for job ${fixture.id}`);
          continue;
        }
        const job = {
          ...fixture, tags, approvalStartedAt, nextPoll: approvalStartedAt,
          status: 'pending_review', queued: false,
        };
        const response = request(reviewer, 'POST', `${job.path}/change-requests/${job.id}/approve`, 'approve_review', tags, {});
        const review = response.status === 200 ? json(response)?.change_request : null;
        if (review?.id === job.id && review.queued_at) markQueued(job);
        // The approval response can be lost after publication. Observe the job without retrying the write.
        pending.add(job);
      }
      nextWave++;
    }

    for (const job of pending) {
      if (Date.now() < job.nextPoll) continue;
      const deadline = job.approvalStartedAt + jobDeadlineMs;
      if (Date.now() < deadline) {
        const phase = phaseAt((Date.now() - started) / 1000);
        const response = request(reviewer, 'GET', `${job.path}/jobs/${job.id}`, 'job_status', { ...job.tags, phase });
        const status = response.status === 200 ? json(response)?.status : null;
        if (status) job.status = status;
        if (['queued', 'running', 'completed', 'failed', 'unresolved'].includes(status)) markQueued(job);
      }

      const status = job.status;
      if (status === 'completed' || status === 'failed' || (job.queued && status === 'cancelled')) {
        const succeeded = status === 'completed';
        record(succeeded ? 'succeeded' : 'failed', job.tags);
        executionFailureRate.add(!succeeded, job.tags);
        completionDuration.add(Date.now() - job.approvalStartedAt, job.tags);
      } else if (Date.now() >= deadline || status === 'rejected' || status === 'cancelled') {
        record(job.queued ? 'unfinished' : 'approval_error', job.tags);
        if (job.queued) executionFailureRate.add(true, job.tags);
      } else {
        job.nextPoll = Math.min(Date.now() + pollIntervalMs, deadline);
        continue;
      }

      if (status !== 'completed') console.warn(`Job ${job.id} ended observation in state ${job.status}`);
      pending.delete(job);
    }

    const nextWaveAt = nextWave < waves.length ? started + waves[nextWave].at * 1000 : Infinity;
    const nextPollAt = Math.min(...Array.from(pending, job => job.nextPoll));
    const nextEvent = Math.min(nextWaveAt, nextPollAt);
    if (nextEvent > Date.now() && Number.isFinite(nextEvent)) sleep((nextEvent - Date.now()) / 1000);
  }
}
