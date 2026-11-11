# Changelog

## 0.1.0-alpha.5

- README rewritten for integrators: requirements, installation, credentials from environment variables, homologation examples that issue, consult and cancel an NF-e and an NFS-e, and the method list per product. The technical details that were in the README moved unchanged to `docs/INTEGRATION.md`, `docs/TESTING.md`, `docs/OPERATIONS.md` and `examples/homologacao/README.md`. Documentation only.
- README: the SDK is presented as available from the public GitHub repository and installed from a clone (`git clone https://github.com/webmaniabr/webmania-go.git`); it is still not published to a package registry. Documentation only: code and contract are unchanged.
- The neutral homologation example (`examples/homologacao`) fails the run (`not_approved`, exit 1) when the consults after the issue do not end on `aprovado`; a refused document, or one still processing when the consults run out, exited 0 before. In both homologation examples a document without a terminal status within the bound is ledgered as `unknown` for reconciliation by consult, instead of its last processing status; a document approved by a later consult is still cancelled and confirmed. Offline subtests cover the refused, stuck and approved-later cases.
- Regenerated from the updated public contract: tax-class list totals (`tributos_federais`, `tributos_estaduais`) accept the empty string the API returns for unset values; tax-class `situacao_tributaria` is 2 digits for IPI, PIS and COFINS and 2 to 3 digits for ICMS; NFS-e `impostos.reducao` and `impostos.cp` accept a string or a number, so their model fields are now `value.Field[value.Scalar]`.
- The homologation example treats `reprovado` (the API 1.0 status of a refused NF-e) as terminal.
- The homologation example requires a writable `WEBMANIA_LIVE_LEDGER` in the `nfe`, `nfse` and `all` scopes (exit 77 with reason `ledger` before any request), so an unknown outcome is always recorded. Its test also covers an unknown issue (ledgered, never resent) and a ledger write that fails mid-run (masked `ledger` step, exit 1).
- `examples/homologation` (one document of any model from a private payload, only with `--execute`) now has the same guards as the neutral cycle: `WEBMANIA_LIVE_ALLOWED_HOSTS` is required before any credential or request, the body that will be sent must carry the JSON integer `2` as its top-level `ambiente` (the text `"2"`, `2.0` and `2e0` were accepted or would be sent as written), and a writable `WEBMANIA_LIVE_LEDGER` is required. The issued document is consulted, an approved one is cancelled (NFCom by its access key), and only a consult showing `cancelado` completes the cleanup; an unknown outcome is ledgered as `unknown` and never resent. Output is one masked JSON line per step. Covered offline by `examples/homologation/main_test.go`, including an unknown issue (ledgered as `unknown` under the payload `ID`, nothing else sent), a cancellation consulted through `processando_cancelamento` until `cancelado`, and a ledger write that fails mid-run (masked `ledger` step, exit 1).
- `scripts/qualify.py` refuses an `--output` that is not absent, empty or left by an earlier run, or that is inside the repository or contains it, before it removes and recreates its `consumer/` and `module-cache/` subdirectories.
- The homologation example requires `WEBMANIA_LIVE_ALLOWED_HOSTS` (exit 77 before any credential or request when it is missing or empty) and fails the run when the consult after a cancellation does not show `cancelado`.
- Generated from the current public contract (14 modules, 103 operations, 102 enabled), pinned by `contract_digest` and per-module SHA-256 instead of a commit; generated files are compared as UTF-8 bytes. Issuance body models follow the operation-derived `<OperationId>Payload` names.
- `DownloadReport` accepts CSV and XLSX labeled `text/xml`, identified by bytes (ZIP signature = XLSX, otherwise CSV kept byte-exact); XML, HTML and JSON bodies are refused. CSV reports are no longer required to be UTF-8: Latin-1 exports are accepted unchanged.
- New `ReportFilesClient.DownloadReportV2` (`fiscalDownloadReportV2`) with explicit access mode; declared report formats must match their bytes.
- New `NewIssuerReportFilesClient`: `DownloadReportV2` with `IssuerFileAuth()` sends the company-scoped API2 Bearer (`ReportCompanyBearer`); `CompanyFileAuth()` still requires company credentials.
- Chunked responses budget each chunk-size line on its own (4000 bytes); only trailers count against the header budget, so large downloads with many chunks no longer fail with `header_limit`. An oversized chunk-size line is a `protocol` error.
- New neutral homologation example (`examples/homologacao`) and tests for report formats, current reports, many chunk-size lines, a stalled successful mutation and the example itself; the packaging script calls `subprocess.run` with explicit `check=False`.

## 0.1.0-alpha.4

- Retain possible quota effects for the public fiscal report GET after request dispatch; preserve no replay, credential separation and capability recovery protection.

## 0.1.0-alpha.3

- Preserve the union of overlapping private literals and UUIDs in diagnostics; distinguish configured literal credentials from short observed URL capability tokens.
- Observe native HTTP trailers within a shared header budget and suppress recovery when capability observation is incomplete.
- Preserve declared public diagnostic member names and structural booleans while protecting fiscal values and private extension keys.
- Reject invalid Go UTF-8 and isolated JSON surrogate escapes before lossy serialization or network work; reject malformed/empty/out-of-range origin ports at construction.
- Own native DNS sockets and lookup lifetime per request; preserve cancellation identity, fresh verified TLS calls and one HTTP attempt.
- Recognize a bounded coherent PDF/XML/ZIP/XLSX subset and verify ZIP inflation sizes/CRC without extraction or external access.

Alpha.2 and alpha.3 artifacts remain historical. Synthetic loopback tests do not establish live fiscal acceptance or publication.
