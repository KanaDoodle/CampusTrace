// Pure response checks shared with the load script. No credentials enter metrics.
export function validSearch(body, user, company, limit) {
  if (!body || !['fulltext', 'basic'].includes(body.mode) || !Array.isArray(body.jobs) || !Array.isArray(body.job_ids) || typeof body.more !== 'boolean') return false;
  if (!body.jobs.length || body.jobs.length > limit || body.jobs.length !== body.job_ids.length || new Set(body.job_ids).size !== body.job_ids.length) return false;
  return body.jobs.every((job, i) => job.id === body.job_ids[i] && job.company === company && job.visibility === 'PRIVATE' && job.owner_id === user);
}
export function validFixture(fixture, run) {
  return fixture?.schema_version === 1 && /^[a-f0-9]{12}$/.test(run || '') && fixture.run_id === run && fixture.base_url === 'http://api:8080' && fixture.users?.length === 2 && fixture.users.every(u => /^[a-f0-9]{32}$/.test(u.id) && typeof u.token === 'string' && u.token.length > 32 && Array.isArray(u.topics) && u.topics.length > 0);
}
