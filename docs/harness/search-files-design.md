# Bounded content search design (#318)

`search_files` is a distinct once-approved disclosure operation. It uses the
existing provider/runtime/UI/sandbox composition and the fixed Linux worker;
it does not chain provider-visible listing and reads. Search remains
unavailable until a search-specific native profile has been measured and
separately reviewed. Listing evidence and the listing availability gate do not
enable search.

## Request and matching semantics

The strict request is `path`, `query`, `recursive`, `include_hidden`,
`include_ignored`, and `name_globs`. The query is nonempty UTF-8, at most 512
bytes, and contains no CR, LF, or NUL. Matching is case-sensitive literal
substring matching, line by line; a query cannot span lines. Line numbers are
1-based. CRLF is treated as one line ending, LF ends a line, and a final line
without LF is searched. Repeated matches on one line produce one result for
that file and line, using the first occurrence. A bounded match snippet keeps
the first occurrence in view; it is rune-aligned and explicitly marked when
the full line was longer.

Only UTF-8 regular files of at most 64 KiB are searchable. NUL-containing or
invalid-UTF-8 files are skipped as unsupported. A file whose identity or
revision changes during search is discarded and makes coverage incomplete.
Oversized and unsupported eligible files increment one aggregate skipped-file
count; protected, hidden, ignored, `.git`, and glob-excluded objects do not
contribute names or counts.

Results are sorted by relative path and line number. At most 100 matching
lines are returned, with 64 KiB for the complete serialized result envelope.
Each snippet is at most 2 KiB. The result includes `incomplete`; hitting a
bound, skipping an eligible file, or encountering a changed file sets it.
`incomplete=false` with no matches means the eligible scope was exhaustively
searched. Search stops after finding a 101st match, after which `incomplete`
is true. The provider receives no raw worker response or private directory
snapshot.

## Preparation, authority, and execution

1. Runtime resolves only the directory path and strictly validates every
   request field and the exact encoded worker frame bound before host
   admission. Admission is operation-specific and binds canonical directory,
   query, all effective discovery options, and provider/model/profile
   destination.
2. The UI's once approval shows that same canonical directory, literal query,
   options, and destination. A listing, selected root, or prior search grants
   no search permission. Changed fields produce a new approval binding.
3. The host retains the selected directory object and checks complete owner
   coverage. A fixed traversal worker returns only a bounded private snapshot
   of relative paths, file identities, directory identities, and ignore-file
   observations. It does not read candidate content.
4. After worker join, the host revalidates every path beneath the retained
   directory descriptor, checks mount/object identities, applies current
   ignore/glob/hidden/`.git` rules, and classifies path, inode, ancestors,
   protected aliases, provider sources, and owned Token Logger objects. Any
   missing protection coverage refuses the affected scope. Protected entries
   are removed before counts or candidate descriptors are created.
5. For each eligible regular file, the host opens and retains a read-only
   descriptor beneath the verified directory, checks its identity and size,
   and records a private revision observation. After all candidate identities
   are classified, the host reads only those retained descriptors and writes
   a bounded content-only snapshot into a sealed anonymous file. It contains
   the query and ordered file bytes, but no names, paths, identities or
   timestamps. The fixed search-content worker receives only this snapshot
   descriptor, cannot open or resolve pathnames, and returns candidate
   indexes, line numbers, bounded snippets, and sanitized skip indexes. The
   host maps indexes to its private path list and never accepts a provider-
   ready result from the worker.
6. After the content worker joins and before staging, the host verifies
   candidate descriptor identity and revision, reopens each candidate path
   beneath the retained directory, checks the current object and protection
   classification again, and records changed candidates as skipped. It then
   validates result indexes, directory observations, policy/owner generations,
   and the admission binding. It attaches host-owned eligible relative paths,
   removes private metadata, enforces result and envelope budgets, and stages the
   result only through the runtime's current authority gate. Owner rotation,
   revocation, cancellation, stale roots, changed objects, worker cleanup
   failure, or stale provider continuation suppresses publication.

This is object authority: a retained object may remain the selected object
after rename, but stale or unverifiable observations are refused or produce an
explicit incomplete result without disclosing stale data. Worker cleanup
ownership remains with the host until all workers and descriptors are joined
or closed. Provider continuation failure never replays a search.

## Bounds and profiles

One search deadline covers traversal, host filtering, descriptor preparation,
worker scans, validation, and result construction: five seconds after once
approval and before provider handoff. One shared budget covers at most 10,000
examined directory entries, 128 eligible candidate files, 8 MiB total file
bytes scanned, 5,000,000 bounded matcher/filter work units, 100 result lines,
and 64 KiB serialized result bytes. Per-file reads remain capped at 64 KiB;
ignore input and rule limits reuse the reviewed listing ceilings. The
128-descriptor cap fits within the transport's existing SCM_RIGHTS limit and
keeps the content scan to one worker. Search input is rejected before
admission if its encoded worker frame exceeds 2,048 bytes. Limits are not
aggregate process, CPU, or memory isolation and no cgroup quota is claimed.

The proposed support boundary for native review is trusted Linux amd64,
Bubblewrap's actual measured `ro-bind-fd` mode, complete owner inventory, and
successful fixed-worker composition. The gate is independent and remains
closed until search-specific native acceptance and independent enablement
review pass. File-snapshot, other architectures, and non-Linux systems remain
unavailable for search.

## Worker operation profiles and tests

Traversal uses a search-specific seccomp profile that permits only the
existing beneath-constrained directory lookup, metadata, and bounded
ignore-file operations. The content worker uses a separate profile that denies
pathname lookup and receives only the sealed content snapshot. Exact file reads
keep their stricter existing profile; no listing or read profile is weakened
for search. The operation profiles are implemented, but the first trusted-host
candidate failed before launching search workers because the public Service
rejected search admission. The next run exercised the search workers and
passed all search cases except cancellation, where the test incorrectly expected
a completed turn instead of the runtime's interrupted outcome and initially
confused the traversal descriptor transfer with the separate content snapshot
handoff. Native acceptance remains incomplete pending a rerun of the corrected
case.

Behavioral tests exercise the runtime-to-provider seam with synthetic content;
the Stage 6 fixture exercises the composed sandbox Service and provider
continuation. Native cases include positive search, protected aliases,
protected candidate substitution by independent same-user actors,
content-worker cancellation/cleanup, root/directory movement, owner rotation,
mount-boundary exclusion, revocation, and stale-publication suppression. All
search-specific cases still require native measurement. Synthetic
runtime-composition outcomes remain separate from native worker enforcement. Production search availability stays false
until the operation-specific results are recorded and reviewed.
