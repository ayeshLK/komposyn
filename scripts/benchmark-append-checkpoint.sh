#!/usr/bin/env bash
set -euo pipefail

baseline_commit='4519551527b28461b250bfdbc7f9f66473a6a219'
repository_root="$(git rev-parse --show-toplevel)"
repeat_count="${BENCH_REPEAT_COUNT:-5}"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-append-bench.XXXXXX")"
baseline_worktree="$temporary_root/baseline"

cleanup() {
	git -C "$repository_root" worktree remove --force "$baseline_worktree" >/dev/null 2>&1 || true
	rm -rf -- "$temporary_root"
}
trap cleanup EXIT

git -C "$repository_root" worktree add --detach "$baseline_worktree" "$baseline_commit"
cp "$repository_root/plugins/library/token-logger/collector_benchmark_test.go" \
	"$baseline_worktree/plugins/library/token-logger/collector_benchmark_test.go"
cp "$repository_root/plugins/library/token-logger/collector_benchmark_observer.go" \
	"$baseline_worktree/plugins/library/token-logger/collector_benchmark_observer.go"

printf '%s\n' "Published v0.7.1 baseline ($baseline_commit), repeated $repeat_count times:"
(
	cd "$baseline_worktree"
	go test ./plugins/library/token-logger -run '^$' -bench '^BenchmarkCollectorGeneratedHistoryScale$' -benchtime=1x -count="$repeat_count"
)
printf '%s\n' "Candidate ($(git -C "$repository_root" rev-parse HEAD)), repeated $repeat_count times:"
(
	cd "$repository_root"
	go test ./plugins/library/token-logger -run '^$' -bench '^BenchmarkCollectorGeneratedHistoryScale$' -benchtime=1x -count="$repeat_count"
)
