# Recursive listing design (#317)

`list_files` keeps `path` as its required argument and adds optional `recursive`,
`include_hidden`, `include_ignored`, and `name_globs` fields. Defaults are false
and an empty glob list. Globs use Git-style wildmatch against each basename;
they filter returned files/directories but never prune traversal. Invalid or
unsupported patterns fail closed. Results are sorted relative paths using `/`.

The runtime canonicalizes only the target path and carries the parsed options
unchanged through admission, approval, execution and publication. Approval
shows the canonical directory, explicit effective values, each glob, and the
selected provider/model/profile. The invocation and admission digest include
the options so changing one requires a new once approval. Exact-file reads
retain their current target eligibility and authorization path.

The existing fixed worker recursively traverses from its single retained root
directory descriptor. It opens descendants relative to the current descriptor
with `openat2` constraints equivalent to `BENEATH`, `NO_SYMLINKS`,
`NO_MAGICLINKS`, and `NO_XDEV`; it never resolves a host pathname or follows a
mount point. The worker returns relative entry paths, object identities, and
directory change observations. After join, the host reopens every reported
component beneath the retained root descriptor, compares identities and mount
IDs, applies current registry protection and owner-coverage checks, and refuses
the entire result if observations are stale or incomplete. This retains
object-authority semantics: a moved selected directory remains the opened
object; descendant replacement is refused rather than followed.

Ignore rules are read as bounded data from safely reopened `.gitignore` files
for each visited directory. Ancestor-to-descendant precedence is preserved;
ignored parents cannot be escaped by child negation. `.git` is excluded at
every depth regardless of options. Protected aliases are removed before any
provider-visible count is computed. `include_ignored` bypasses ignore filtering
only; it does not bypass `.git`, hidden defaults, protected-resource policy,
owner coverage, symlink checks, or mount checks. Hidden inclusion is separate.

One shared invocation budget covers worker traversal and host filtering:
10,000 examined entries, 1,000 returned paths, 64 KiB serialized output, five
seconds wall time, the existing aggregate matcher-work ceiling, and bounded
ignore bytes/rule counts (up to 64 KiB for the selected-root ancestor chain and
64 KiB total for nested files). Raw counts that include hidden/protected
objects remain private. Results carry an incomplete flag if a safe limit stops
coverage; budget exhaustion never publishes unfiltered or exhaustive results.

The intended supported profile is the existing trusted Linux Bubblewrap
profile. The current environment's `/usr/bin/bwrap` is owned by `nobody`, so it
does not satisfy the repository's trusted-backend policy. Recursive
advertisement must remain withheld until the extended native runner passes
the descendant-race, alias/protection, mount, cancellation and cleanup cases on
the tested source revision. Existing nonrecursive evidence does not enable
recursion.
