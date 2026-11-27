import { requestHref } from "../../migration/api.js";
import { RequestBadge } from "./ChangeRequestParts.jsx";

export function BlockingJobs({ databaseId, jobs }) {
  if (!jobs.length) return null;
  return (
    <div className="alert notice warning">
      <p>Possible blocking jobs on this database (the server does not identify the exact blocker):</p>
      <ul>
        {jobs.map((job) => <li key={job.id}>
          <a className="cr-link" href={requestHref(databaseId, job.id)}>{job.migration_name || job.migration_id} · {job.id}</a>{" "}
          <RequestBadge status={job.status} />{job.paused && <small> (paused)</small>}
        </li>)}
      </ul>
      <p>Later jobs wait until the database is released. Other databases are unaffected.</p>
    </div>
  );
}
