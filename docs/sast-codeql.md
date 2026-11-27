# Static Application Security Testing (SAST) with CodeQL

## 1. Overview

Static Application Security Testing (SAST) examines source code without requiring the application to be running. RuangSuara uses GitHub CodeQL to identify potentially insecure code patterns and trace data from sources to security-sensitive operations. GitHub Actions automates the analysis for the Go backend and JavaScript/JSX frontend.

The purpose of this report is to document the scan evidence, all reported findings, their potential impact, and the proposed decisions and follow-up actions. A CodeQL result is a candidate vulnerability, not automatic proof of exploitability.

**Report status:** Initial scan verified; source-based triage proposed; remediation, regression testing, GitHub alert dispositions, and re-scan verification pending.

## 2. Implementation and configuration

Repository: [Celtors/IF3110_K03_09_RuangSuara_v2](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2).

| Configuration | Implementation |
| --- | --- |
| Workflow | [`.github/workflows/codeql.yml`](../../.github/workflows/codeql.yml) |
| Query configuration | [`.github/codeql/codeql-config.yml`](../../.github/codeql/codeql-config.yml) |
| Automation | GitHub Actions, `ubuntu-latest` |
| Triggers | Pushes and pull requests targeting `main` or `feat/**`; weekly schedule; manual dispatch |
| Schedule | `23 3 * * 1` (Monday 03:23 UTC / 10:23 WIB, default branch only) |
| Languages | `go`, `javascript-typescript` |
| Go build | Manual: `go build ./...` in `backend/src`, Go 1.26.1, C compiler installed, `CGO_ENABLED=1` |
| JavaScript build | None; source extraction without Vite build or npm installation |
| Query suite | Default queries retained; `security-extended` added for broader security coverage |
| Exclusions | `frontend/dist/**`, `frontend/dist-ssr/**`, `frontend/coverage/**`, `**/node_modules/**` (generated output and installed dependencies) |
| Evidence | SARIF uploaded to GitHub Code scanning and preserved with metadata as workflow artifacts (30-day retention) |

The `security-extended` suite broadens the set of security checks beyond the default suite. Additional findings may require more manual review. No security rule suppression is configured in the supplied workflow. Test source is not deliberately excluded by the paths-ignore configuration; actual extraction coverage is discussed below.

## 3. Verified initial scan

The baseline for this report is the **push scan on `feat/integrate`**, not the earlier pull-request scan.

| Evidence field | Verified value |
| --- | --- |
| Workflow run | [CodeQL SAST — run 46012284127](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/actions/runs/46012284127) |
| Run attempt | 1 |
| Event | `push` |
| Git ref | `refs/heads/feat/integrate` |
| Scanned commit | [`e68735b2d9dae5efd4ae1c4b3a27fc94eca76e47`](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/commit/e68735b2d9dae5efd4ae1c4b3a27fc94eca76e47) |
| Scan date | 27 November 2026 (UTC) |
| JavaScript metadata timestamp | 14:02:40 UTC |
| Go metadata timestamp | 14:04:29 UTC |
| CodeQL CLI | 2.27.6 |
| Query suite | Default + `security-extended` |
| Analysis mode | Full push analysis; both logs state that the diff-informed stage was skipped because no precomputed diff ranges existed |
| Backend build | Completed, allowing Go analysis to proceed |
| Go / JavaScript analysis | Both SARIF invocations report `executionSuccessful: true` |
| Code scanning upload | Both jobs report `Successfully uploaded results` and `Analysis upload status is complete.` |
| Artifact upload | Both artifacts successfully finalized |

The repository ZIP identifies its HEAD as `feat/integrate` at the same SHA as both SARIF metadata files. The source locations reviewed below match the supplied snapshot; uncommitted worktree changes and individual Git blobs have not been independently verified. The GitHub UI alert state was not independently inspected.

### 3.1 Evidence files

Retain the original downloaded archives under `docs/security/evidence/initial/` (or another team-approved evidence location). The links below identify the supplied files after that placement; they are not included in this Markdown file.

| Evidence | Contents |
| --- | --- |
| [`codeql-go-46012284127-1.zip`](evidence/initial/codeql-go-46012284127-1.zip) | `sarif/go.sarif`, `metadata.json`; artifact ID `15204980187` |
| [`codeql-javascript-typescript-46012284127-1.zip`](evidence/initial/codeql-javascript-typescript-46012284127-1.zip) | `sarif/javascript.sarif`, `metadata.json`; artifact ID `15204328369` |
| [`logs_124479857497.zip`](evidence/initial/logs_124479857497.zip) | Original workflow logs for both matrix jobs |

The supplied download names had a local `(2)` suffix; the intended evidence filenames above omit that suffix. Preserve the original bytes when renaming. SHA-256 checksums of the supplied archives:

```
fad5b4bc14fa02b8ee8c3171bd5f6bf6dfdba52569c31cce685e574d2b3c2e46  codeql-go-46012284127-1.zip
3dc5a6530268f0fb7f850ce24ec9bc47208ddcf74f5b671b0f374a1b1c01c2cb  codeql-javascript-typescript-46012284127-1.zip
f2a668ab1b7bf0d2af8da964f6f2761dfa81b7e02427598b3f3e9a9e48e0e7aa  logs_124479857497.zip
```

Relevant log entries are found in each `Analyze (<language>)/8_Analyze and upload to code scanning.txt` file and each `9_Preserve SARIF and metadata.txt` file. Before committing evidence, review archives for credentials, sensitive logs and unintended source exposure. Do not commit `.env`.

**Screenshot placeholders:**

- [ ] TODO: Workflow summary showing both successful analysis jobs, run URL, branch and commit.
- [ ] TODO: Code scanning dashboard filtered to `feat/integrate` showing the findings.
- [ ] TODO: Tool status / coverage view for both languages.
- [ ] TODO: Individual alert detail screenshots where useful.

## 4. Analysis coverage

Coverage figures are taken from SARIF metric results, extraction diagnostics and the Actions logs, not estimated from repository size.

| Metric | Go | JavaScript/TypeScript |
| --- | ---: | ---: |
| Lines of code in CodeQL database | 8,209 | 6,798 |
| User-written lines (separate metric) | Not reported | 6,768 |
| Successfully extracted language source files | 55 `.go` | 28 `.js` + 60 `.jsx` |
| Log-reported coverage | 55 of 65 Go files | 28 of 28 JavaScript files |
| Security findings | 6 | 0 |

The JavaScript diagnostic also identifies 12 other extracted files (YAML/YML, HTML and JSON), for 100 distinct diagnostic file paths. The Go diagnostic lists 57 distinct paths (55 Go files, one HTML file and `go.mod`). Incidental extraction of configuration does not establish dedicated GitHub Actions, YAML, SQL or infrastructure security coverage. No separate Actions language job was configured.

**Go coverage limitation:** The Go logs report 55 of 65 expected Go files scanned (84.6%). Comparing `cli/expected-extracted-files/go` with `go/diagnostics/successfully-extracted-files` in the Go SARIF identifies the following ten test files absent from the successful extraction list:

- `backend/src/access_control/effect/effect_extended_test.go`
- `backend/src/access_control/effect/effect_test.go`
- `backend/src/database_management/accessible_test.go`
- `backend/src/database_management/models_test.go`
- `backend/src/database_management/provider_test.go`
- `backend/src/migration/job_integration_test.go`
- `backend/src/migration/migration_test.go`
- `backend/src/migration/query_integration_test.go`
- `backend/src/migration/query_test.go`
- `backend/src/migration/review_test.go`

Although the workflow sets `CODEQL_EXTRACTOR_GO_OPTION_EXTRACT_TESTS=true`, this run does not demonstrate successful extraction of those test files. The manual `go build ./...` builds non-test packages; investigate the extraction configuration and a supported test-build approach, then verify actual coverage in a subsequent run. Do not assume the environment variable alone proves test coverage.

**Frontend result:** CodeQL returned zero JavaScript/TypeScript findings. This means no findings were produced by the configured queries for the extracted code at this commit; it does not establish that the frontend is secure.

## 5. Initial findings

The Go SARIF contains six results across three rule IDs. The JavaScript SARIF contains zero results. Each SARIF result is counted once; the four paths associated with the SQL injection result are not four separate alerts.

| Security severity | Go | JavaScript | Total |
| --- | ---: | ---: | ---: |
| Critical (9.0–10.0) | 0 | 0 | 0 |
| High (7.0–8.9) | 1 | 0 | 1 |
| Medium (4.0–6.9) | 5 | 0 | 5 |
| Low (0.1–3.9) | 0 | 0 | 0 |
| **Total** | **6** | **0** | **6** |

Severity bands use CodeQL's `security-severity` metadata, not the separate diagnostic `problem.severity` field. The scores are CodeQL rule scores, not newly calculated application-specific CVSS assessments.

The following IDs are local report identifiers, not GitHub alert numbers. Decisions are **proposed** based on source review; no finding has been dismissed or fixed as part of preparing this report.

| ID | Rule | Security severity | Code location (relative to repository root) | Potential impact | Proposed decision and rationale |
| --- | --- | --- | --- | --- | --- |
| SAST-01 | [`go/cookie-secure-not-set`](https://codeql.github.com/codeql-query-help/go/go-cookie-secure-not-set/) | Medium (4.0) | `backend/src/access_control/session_manager/logout.go:61` | Logout cookie is emitted without the Secure attribute; inconsistent cookie handling. | **Fix.** Login sets Secure but logout does not; align deletion attributes. Empty value reduces direct confidentiality impact. |
| SAST-02 | [`go/sql-injection`](https://codeql.github.com/codeql-query-help/go/go-sql-injection/) | High (8.8) | `backend/src/database_management/handlers.go:452` | If identifier escaping is bypassed, unauthorized reads or other SQL operations may occur within target database privileges. | **Candidate false positive; verification required.** PostgreSQL identifiers are quoted and embedded double quotes doubled; metadata existence check and bound pagination provide additional controls. Verify the actual driver path and adversarial tests before dismissal. |
| SAST-03 | [`go/log-injection`](https://codeql.github.com/codeql-query-help/go/go-log-injection/) | Medium (6.1) | `backend/src/internal/telemetry/http.go:46` | Forged panic log entries could mislead incident response. | **Candidate false positive.** Logged route comes from Gin's registered route pattern, not the raw request path. |
| SAST-04 | [`go/log-injection`](https://codeql.github.com/codeql-query-help/go/go-log-injection/) | Medium (6.1) | `backend/src/internal/telemetry/http.go:74` | Forged request telemetry could undermine monitoring and audit integrity. | **Candidate false positive.** Same registered-route source; HTTP method is separately normalized. |
| SAST-05 | [`go/log-injection`](https://codeql.github.com/codeql-query-help/go/go-log-injection/) | Medium (6.1) | `backend/src/database_management/handlers.go:55` | Forged database failure records could conceal unauthorized requests or create misleading evidence. | **Candidate false positive.** The flagged expression is `c.FullPath()`, not the user-supplied database ID or raw URL. |
| SAST-06 | [`go/log-injection`](https://codeql.github.com/codeql-query-help/go/go-log-injection/) | Medium (6.1) | `backend/src/migration/http.go:133` | Forged migration failure records could compromise audit integrity. | **Candidate false positive.** The flagged expression is the registered route pattern. |

### 5.1 SAST-01 — Cookie 'Secure' attribute is not set to true

- **Rule:** `go/cookie-secure-not-set`; CWE-614; CodeQL problem severity `warning`, precision `high`.
- **Location:** `backend/src/access_control/session_manager/logout.go:61`, `LogoutRequest`.
- **Evidence:** SARIF message: `Cookie does not set Secure attribute to true.`

```go
c.SetCookie("ssid", "", -1, "/", "", false, true)
```

The fifth argument is the cookie domain, the sixth is Secure (`false`), and the seventh is HttpOnly (`true`). The cookie is empty and expired to delete the session cookie. The server also attempts to revoke the session in the database (`logout.go:52–59`). At login, `login.go:116–117` sets `SameSite=Lax`, uses an empty domain (host-only), path `/`, Secure and HttpOnly.

**Impact:** A cookie without Secure can be transmitted over HTTP, but this particular response contains an empty deletion value, not the session token. The direct token-disclosure consequence described by the generic rule is therefore not demonstrated for this sink. Inconsistent security attributes nevertheless warrant correction and verification of logout behavior.

**Proposed decision: Fix.** Match the login cookie attributes, preserving the host-only domain. Proposed code (not yet applied):

```go
c.SetSameSite(http.SameSiteLaxMode)
c.SetCookie("ssid", "", -1, "/", "", true, true)
```

**Verification plan:** Test logout over the supported deployment transport, confirm `Set-Cookie` contains Secure, HttpOnly, SameSite=Lax, path `/`, no Domain and an expiration/deletion directive. Confirm browser cookie removal and server-side session revocation. Also review the existing behavior if database revocation fails; that is separate from this CodeQL rule.

- Fix commit: TODO — SHA and link.
- Test result: TODO.
- Re-scan result: TODO.

### 5.2 SAST-02 — Database query built from user-controlled sources

- **Rule:** `go/sql-injection`; CWE-89; CodeQL problem severity `error`, precision `high`.
- **Location:** `backend/src/database_management/handlers.go:452`, `Handler.rows`.
- **Evidence:** One result with two user-controlled sources and four reported data-flow paths.

**Source-to-sink:** `c.Param("schema")` and `c.Param("table")` at line 426 → custom `quote` function at line 448 → `fmt.Sprintf` at line 450 → `db.Raw(query, limit, offset)` at line 452.

Relevant source:

```go
quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
query := fmt.Sprintf("SELECT * FROM %s.%s LIMIT ? OFFSET ?", quote(schema), quote(table))
var rows []map[string]interface{}
if err := db.Raw(query, limit, offset).Scan(&rows).Error; err != nil {
    // Error handling omitted.
}
```

**Existing controls:**

- `handlers.go:46–52` registers the viewer endpoint under session and database view-permission middleware. `requireView` checks the database ID and permission at lines 133–147.
- `handlers.go:428–435` performs a parameterized metadata existence check for the requested schema/table. This is useful validation but is not a substitute for identifier escaping.
- `handlers.go:448` surrounds each identifier with double quotes and doubles embedded double quotes, consistent with PostgreSQL identifier syntax.
- Pagination is passed as bound values and checked at lines 436–452.
- `backend/src/database_management/provider.go`, `Provider.Target`, uses the PostgreSQL GORM driver in the supplied code. No alternative SQL dialect is established for this path.

**Impact:** If an attacker could break out of the identifier context, the target database connection could execute unintended SQL, potentially exposing or modifying data subject to driver behavior and target database privileges. Database-level view authorization does not itself prevent SQL injection.

**Proposed decision: Candidate false positive, not yet approved.** The reported flow reaches a SQL sink but the query uses PostgreSQL identifier escaping. A syntactically valid identifier can still contain unusual characters, and the exact GORM placeholder-processing and PostgreSQL execution path has not been tested here. Prioritize verification because the rule is High severity.

**Verification plan:** Use a controlled PostgreSQL test database with ordinary and unusual schema/table identifiers (quotes, dots, question marks and whitespace). Confirm that only the intended table is queried, pagination remains correctly bound, invalid or nonexistent identifiers are rejected, and unauthorized database access fails. Verify that the SQL construction does not reinterpret identifier content as SQL syntax or placeholders. If the current implementation fails, replace it with a verified identifier construction and execution approach compatible with the driver, such as `pgx.Identifier.Sanitize()` with appropriate native parameter binding; add tests and re-scan. Do not use value placeholders as a substitute for SQL identifier quoting.

- Final disposition: TODO — confirm false positive or implement a fix.
- Test result / evidence: TODO.
- Fix commit (if needed): TODO.
- GitHub alert number and dismissal (if justified): TODO.
- Re-scan result: TODO.

### 5.3 SAST-03 to SAST-06 — Log entries created from user input

All four results use `go/log-injection`, CWE-117, CodeQL problem severity `error`, precision `medium`, and a security score of 6.1. The potential weakness is insertion of forged or misleading log records by attacker-controlled input.

| ID | Source and sink | Context-specific assessment |
| --- | --- | --- |
| SAST-03 | `telemetry/http.go:25` (`c.FullPath()`) → `route` → `slog.ErrorContext` at line 46 | Panic recovery logs the matched route pattern. The panic value and raw request URL are not the flagged input. |
| SAST-04 | `telemetry/http.go:25` → `route` → `slog.Log` at line 74 | Request completion logs the matched route pattern. `httpMethod` at lines 80–88 restricts the method to known methods or `OTHER`. |
| SAST-05 | `database_management/handlers.go:55` (`c.FullPath()` directly in `slog.WarnContext`) | Database request failure logs the route pattern; route parameters are not directly inserted by this expression. |
| SAST-06 | `migration/http.go:133` (`c.FullPath()` directly in `slog.WarnContext`) | Migration request failure logs the route pattern, not the raw migration SQL or request body. |

The SARIF contains explicit code flows for SAST-03 and SAST-04; SAST-05 and SAST-06 contain a related source location but no separate code-flow list.

**Existing controls and reasoning:** Gin's `Context.FullPath()` represents the registered route template (or an empty string for an unmatched route), not the raw path containing attacker-controlled parameters. The supplied route registration code uses static templates. The local logger is configured with `slog.NewJSONHandler` in `backend/src/internal/telemetry/log.go:63–77`, which encodes string values in structured JSON. `main.go:31–32` installs the application logger; `internal/telemetry/export.go:44–58` optionally adds a structured OTLP logging bridge. The JSON encoding is a secondary control for the local sink and should not be assumed to prove every downstream exporter or viewer is safe.

**Proposed decision for each of SAST-03, SAST-04, SAST-05 and SAST-06: Candidate false positive.** The specific flagged route expression is expected to be a server-defined string. Preserve useful telemetry rather than removing log statements just to silence the rule. Confirm that dynamic route registration or other application-specific behavior cannot introduce untrusted route templates. This decision is limited to the flagged `route` expressions; it does not certify every other log attribute.

**Verification plan:** Exercise the actual Gin middleware and handlers with attacker-controlled route parameters (including encoded control characters where the server accepts them), unmatched routes, forced panic and normal completion, database request failures and migration failures. Capture structured logs; verify that route attributes equal registered templates or an empty value, every local log record parses as one JSON object, and no forged record appears. If OTLP is enabled, verify the bridge and downstream consumer preserve record boundaries and do not reinterpret strings unsafely.

| ID | Required test evidence | Final disposition / alert reference |
| --- | --- | --- |
| SAST-03 | TODO — panic log regression test | TODO |
| SAST-04 | TODO — completion / unmatched-route regression test | TODO |
| SAST-05 | TODO — database failure log regression test | TODO |
| SAST-06 | TODO — migration failure log regression test | TODO |

## 6. Remediation and triage tracking

No security fix was implemented, committed, tested or re-scanned during preparation of this report. The initial scan SHA is not a remediation commit.

| ID | Proposed action | Final decision | Fix commit | Validation evidence | Status after re-scan |
| --- | --- | --- | --- | --- | --- |
| SAST-01 | Fix logout cookie attributes | Fix proposed; implementation pending | TODO | TODO | TODO |
| SAST-02 | Verify SQL identifier handling; fix if unsafe | TODO | TODO / N/A if confirmed false positive | TODO | TODO |
| SAST-03 | Verify route template and panic logging | TODO | TODO / N/A if confirmed false positive | TODO | TODO |
| SAST-04 | Verify route template and request logging | TODO | TODO / N/A if confirmed false positive | TODO | TODO |
| SAST-05 | Verify route template and database failure logging | TODO | TODO / N/A if confirmed false positive | TODO | TODO |
| SAST-06 | Verify route template and migration failure logging | TODO | TODO / N/A if confirmed false positive | TODO | TODO |

Once verification is complete, record the actual GitHub alert number and the final decision for every result. For a confirmed false positive, document the concrete control, test results and GitHub dismissal reason `False positive` (where the dashboard permits dismissal). For a valid weakness, fix it rather than dismissing the alert to make the dashboard green.

### 6.1 Accepted risks

No accepted risk is claimed in this initial report. If a valid finding is intentionally not fixed, complete the following record:

| Field | Value |
| --- | --- |
| Finding ID / GitHub alert | TODO, if applicable |
| Reason for accepting risk | TODO |
| Compensating control and evidence | TODO |
| Residual impact and likelihood | TODO |
| Risk owner / approval | TODO |
| Revisit date | TODO |
| GitHub disposition | TODO |

### 6.2 Test and build verification

| Command / check | Initial evidence | After remediation |
| --- | --- | --- |
| `cd backend/src && go build ./...` | Completed during CodeQL job | TODO |
| `cd backend/src && go vet ./...` | Not established by supplied logs | TODO |
| `cd backend/src && go test ./...` | Not established by supplied logs | TODO |
| `cd frontend && npm ci` | Not required by this CodeQL job | TODO |
| `cd frontend && npm run lint` | Not established by supplied logs | TODO |
| `cd frontend && npm test` (or CI run mode) | Not established by supplied logs | TODO |
| `cd frontend && npm run build` | Not established by supplied logs | TODO |
| Focused cookie, SQL and logging tests | Not run for this report | TODO |

A successful CodeQL analysis is not equivalent to passing application tests.

## 7. Re-scan and final evidence

After implementing required fixes and completing triage, push to a branch containing the CodeQL workflow and matching its trigger filters. Prefer a full push scan of the same integration branch and query configuration for a comparable baseline.

| Metric | Initial scan | Post-remediation scan |
| --- | --- | --- |
| Run URL | [46012284127](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/actions/runs/46012284127) | TODO |
| Branch | `feat/integrate` | TODO |
| SHA | `e68735b2d9dae5efd4ae1c4b3a27fc94eca76e47` | TODO |
| Date | 2026-11-27 | TODO |
| CodeQL version | 2.27.6 | TODO |
| Query suite | Default + `security-extended` | TODO |
| Go raw SARIF findings | 6 | TODO |
| JavaScript raw SARIF findings | 0 | TODO |
| High / Medium raw findings | 1 / 5 | TODO |
| Confirmed fixed alerts | Not yet verified | TODO |
| Confirmed false-positive dismissals | Not yet verified | TODO |
| Approved accepted risks | None claimed | TODO |
| Newly introduced alerts | Not applicable to baseline | TODO |
| Go coverage | 55 / 65 expected Go files | TODO |

A false-positive dismissal may change the GitHub dashboard state without removing the corresponding result from a later raw SARIF file. Report raw findings and dashboard dispositions separately. Match each finding using its rule, code location and, where available, SARIF fingerprint or GitHub alert number; do not rely only on aggregate counts.

### 7.1 Evidence collection instructions

1. Open the [Actions page](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/actions), choose the new CodeQL SAST run, and confirm both language jobs succeeded.
2. From the run Summary, download both SARIF artifacts and the workflow logs. Save them under `docs/security/evidence/rescan/`; record actual filenames, URLs, commit SHA, version and date.
3. Open [Code scanning](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/security/code-scanning) and filter to the scanned branch (`feat/integrate` for the baseline). Review alert dispositions and [tool coverage](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/security/code-scanning/tools/CodeQL/status/).
4. Complete the tracking table and verify the required fixes are absent in the re-scan, not merely hidden by a filter. Document new findings and remaining coverage gaps.
5. Add screenshots and commit links.

Optional GitHub CLI commands (replace `RUN_ID` with the real re-scan ID; authentication and repository permissions are required):

```bash
gh run list --repo Celtors/IF3110_K03_09_RuangSuara_v2 --branch feat/integrate
gh run view RUN_ID --repo Celtors/IF3110_K03_09_RuangSuara_v2 --log
gh run download RUN_ID --repo Celtors/IF3110_K03_09_RuangSuara_v2 --pattern 'codeql-*'
gh api --paginate 'repos/Celtors/IF3110_K03_09_RuangSuara_v2/code-scanning/alerts?ref=refs/heads/feat/integrate&per_page=100'
```

- TODO: Post-remediation workflow screenshot.
- TODO: Post-remediation SARIF/log evidence links.
- TODO: Alert detail / closure or justified dismissal screenshots.
- TODO: Final reviewer approval and date.

## 8. Limitations and conclusion

The supplied evidence demonstrates that CodeQL SAST is enabled and successfully analyzed the Go and JavaScript/JSX code on `feat/integrate`. Six backend findings were reported, including one High-severity SQL injection candidate and five Medium-severity findings. No frontend findings were reported. Source review suggests one cookie configuration fix and five potential false positives that require verification; none is claimed as remediated or formally dismissed.

Static analysis is limited by extraction coverage, query scope, framework modeling and available code. It cannot by itself establish runtime configuration safety, correct business logic and authorization design, dependency CVE coverage, or resistance to dynamic attacks. In particular, the ten missing Go test files and the absence of frontend findings must not be interpreted as complete security assurance. Dependency analysis, dynamic testing and manual security review are complementary activities.

**A clean CodeQL result does not prove that the application is secure.** The assignment's remediation and re-scan requirements remain open until the TODO entries are completed with real evidence.

## References

- [OWASP — Static Application Security Testing](https://owasp.org/www-community/Source_Code_Analysis_Tools)
- [CodeQL query help — Go](https://codeql.github.com/codeql-query-help/go/)
- [Initial workflow run](https://github.com/Celtors/IF3110_K03_09_RuangSuara_v2/actions/runs/46012284127)
- Primary evidence: supplied Go and JavaScript SARIF archives, workflow logs and repository source snapshot, as identified in Section 3.
