// Empty results are valid only for the deliberately absent query. A sparse
// query must return the exact synthetic job, with Core ownership preserved.
export function validDiagnosis(body, user, company, mode, kind, sparseID) {
  const jobs = body?.jobs, ids = body?.job_ids;
  if (body?.mode !== mode || typeof body.more !== 'boolean' || !Array.isArray(jobs) || !Array.isArray(ids)) return false;
  if (jobs.length !== ids.length || new Set(ids).size !== ids.length) return false;
  if (!jobs.every((j, n) => j.id === ids[n] && j.visibility === 'PRIVATE' && j.owner_id === user && j.company === company)) return false;
  if (kind === 'common') return jobs.length === 10;
  if (kind === 'sparse') return jobs.length === 1 && ids[0] === sparseID;
  return kind === 'miss' && jobs.length === 0;
}
