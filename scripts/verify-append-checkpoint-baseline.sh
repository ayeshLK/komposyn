#!/usr/bin/env bash
set -euo pipefail

baseline_commit='4519551527b28461b250bfdbc7f9f66473a6a219'
repository_root="$(git rev-parse --show-toplevel)"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-append-oracle.XXXXXX")"
baseline_worktree="$temporary_root/baseline"
baseline_output="$temporary_root/baseline.json"
candidate_output="$temporary_root/candidate.json"

cleanup() {
	git -C "$repository_root" worktree remove --force "$baseline_worktree" >/dev/null 2>&1 || true
	rm -rf -- "$temporary_root"
}
trap cleanup EXIT

if ! git -C "$repository_root" cat-file -e "$baseline_commit^{commit}" 2>/dev/null; then
	git -C "$repository_root" fetch --no-tags https://github.com/ayeshLK/komposyn.git "$baseline_commit"
fi

git -C "$repository_root" worktree add --detach "$baseline_worktree" "$baseline_commit"
cp "$repository_root/plugins/library/token-logger/streaming_baseline_oracle_test.go" \
	"$baseline_worktree/plugins/library/token-logger/streaming_baseline_oracle_test.go"

(
	cd "$baseline_worktree"
	KOMPOSYN_STREAMING_ORACLE_OUTPUT="$baseline_output" \
	KOMPOSYN_STREAMING_ORACLE_ROOT="$temporary_root/fixtures" \
		go test ./plugins/library/token-logger -run '^TestStreamingBaselineOracle$' -count=1
)
(
	cd "$repository_root"
	KOMPOSYN_STREAMING_ORACLE_OUTPUT="$candidate_output" \
	KOMPOSYN_STREAMING_ORACLE_ROOT="$temporary_root/fixtures" \
		go test ./plugins/library/token-logger -run '^TestStreamingBaselineOracle$' -count=1
)

if ! diff -u "$baseline_output" "$candidate_output"; then
	echo "append/checkpoint behavior differs from published v0.7.1 baseline $baseline_commit" >&2
	exit 1
fi

echo "append/checkpoint observable behavior matches published v0.7.1 baseline $baseline_commit"
