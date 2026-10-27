<!-- Copyright 2026 Ayesh Almeida -->
<!-- Licensed under the Apache License, Version 2.0. -->

# Issue #164 final acceptance matrix

This matrix records the evidence present on the final acceptance branch. A
feature being implemented does not by itself count as tested evidence. Fixture
tests use fake credentials and local endpoints; no live OAuth login or billable
inference is claimed.

## Reliability and process boundaries

| Acceptance requirement | Evidence | Status / limit |
| --- | --- | --- |
| A canceled lock waiter returns without mutation while another OS process owns the native lock; the owner remains exclusive until release. | `internal/harness/auth/store_process_test.go`: `TestNativeAuthLockExcludesAndCancelsAcrossProcesses` | **Implemented and tested** on the local Unix runner; native Windows execution is delegated to the hosted Windows CI matrix. |
| Two processes resolving the same expired profile perform one token exchange and both observe the saved replacement. | `internal/harness/auth/openai/process_test.go`: `TestConcurrentRefreshAcrossProcessesUsesOneReplacement` | **Implemented and tested** with a local fake token endpoint on the local Unix runner. Native Windows result pending hosted CI. |
| A consumed refresh request with a lost response persists an uncertain-outcome marker; another process refuses replay. | `internal/harness/auth/openai/process_test.go`: `TestUnknownRefreshOutcomeSurvivesProcessAndBlocksReplay` | **Implemented and tested** with an HTTP endpoint that accepts the request then drops the connection. It cannot recover a replacement token whose response was never received. |
| Stale reconnect cannot restore a disconnected or removed profile across processes. | `internal/harness/auth/store_process_test.go`: `TestOAuthLifecycleMutationWinsAcrossProcesses`; in-process generation checks in `store_test.go` | **Implemented and tested** for disconnect and removal orderings on the local Unix runner. Native Windows result pending hosted CI. |
| Caller cancellation after refresh starts does not cancel token rotation persistence; shutdown drains owned refresh work. | `internal/harness/auth/openai/oauth_test.go`: `TestCanceledCallerWaitsNoLongerButRefreshPersistsRotation`, `TestRefreshRequestUsesIndependentBoundedTimeout` | **Implemented and tested** in-process. Process termination can occur before the rotated response or uncertain marker is persisted; reauthentication may then be required. |
| Competing goroutines/processes, temporary/terminal errors, and no inference after canceled refresh. | `oauth_test.go`: `TestConcurrentRefreshAcrossProcessesUsesOneReplacement`, `TestConfirmedTemporaryRefreshRejectionPreservesCredentialState`, `TestInvalidGrantRequiresReconnectWithoutDeletingRegistration`; `runtime/chat_test.go`: `TestCanceledChatRefreshDoesNotStartInferenceOrAppendTurn` | **Implemented and tested** with deterministic fakes. |

## Auth lifecycle, registry, and UI

| Acceptance requirement | Evidence | Status / limit |
| --- | --- | --- |
| Successful login validates identity, persists credentials, publishes safe completion/status, and does not choose a model or send inference. | `internal/harness/auth/openai/oauth_test.go`: `TestLoginRegistersAndStoresOnlyValidatedCandidate`; composition path in `internal/harness/runtime/provider_walkthrough_test.go`: `TestOfflineProviderWalkthroughBothProviders` | **Partially tested.** Identity, persistence, permissions, and fake provider walkthrough are covered; no single end-to-end test currently asserts all of progress, model selection, and inference request counts together. |
| Denied/canceled login and identity login without inference permission remain distinct and preserve a previously valid profile. | `oauth_test.go`: `TestLoginCancellationAndWrongStateDoNotPersistCandidate`; `runtime/chat_test.go`: `TestOAuthIdentityWithoutInferencePermissionCannotStartChat` | **Implemented and tested** for denied login preserving an existing profile and for identity-only status blocking inference through runtime. UI rendering of the distinct denied outcome is covered by the general non-success screen behavior, not a full live OAuth screen test. |
| Reconnect targets the intended registration; model/account changes retain existing context-reset semantics. | `auth/store_test.go`: `TestOAuthLifecycleGenerationRejectsStaleReconnects`; `runtime/chat_test.go`: `TestOAuthAccessTokenRefreshKeepsSelectedModelAndChatContext`, `TestTemporaryMultiMessageChatAndConfirmedModelReset` | **Implemented and tested.** Process ordering for disconnect/removal is additionally tested above. |
| Disconnect retains the registration while clearing local tokens; removal deletes it; remote revocation uncertainty is reported accurately. | `oauth_test.go`: `TestDisconnectReportsRemoteRevocationAndRemovesLocalTokens`; `auth/store_test.go`: lifecycle mutation tests | **Implemented and tested** for provider revocation result and local storage outcomes. |
| Cancel during auth/store waits and suppress stale UI updates; shutdown remains bounded and responsive. | `runtime/chat_test.go`: `TestSnapshotAndCancelStayResponsiveDuringAuthStoreLockWait`, `TestCanceledChatRefreshDoesNotStartInferenceOrAppendTurn`; `plugins/ui/tview/ui_test.go`: `TestStopDrainsQueuedWorkerUpdates`, `TestCancelButtonCancelsPendingAddOperation`, `TestCancelButtonCancelsPendingReplaceOperation`, `TestProviderAdvertisedOAuthMethodCanBeCanceled` | **Implemented and tested.** TUI tests use an input-driven simulated screen. |
| Explicit API-key and environment-reference profiles for supported providers never fall back to another credential. | `auth/registry_test.go`: `TestExplicitEnvironmentProfilesResolveOnlyForTheirProvider`, `TestMissingExplicitEnvironmentCredentialIsConfigurationFailure`; `model/catalog_test.go`: `TestDiscoveryKeepsProfilesSeparateAndPartialFailuresVisible`; runtime fake-provider walkthrough | **Partially tested.** Both providers and explicit resolution are exercised, and registry routing prevents cross-provider fallback. A complete missing/failed selected-credential matrix across discovery and inference remains **unverified**. |
| Public status, progress, snapshots, errors, and browser capability do not expose secrets or authorization URL hints; Manage usage invokes the injected opener. | `oauth_test.go`: URL-free progress, sanitized browser/open-usage failures; `auth/registry_test.go`: typed sanitized errors; `runtime/chat_test.go`: sanitized request failure; PR #166 regression `TestOpenUsageUsesInjectedBrowserAndSanitizesErrors` | **Implemented and tested** for these public output boundaries. |
| `/model` navigation preserves draft/focus and responsive progress/cancel behavior. | `plugins/ui/tview/ui_test.go`: `TestPickerInteractionOnSimulationScreen`, `TestEscapeFromModelFilterReturnsToChatAndPreservesSelection`, cancellation and stop-drain cases | **Implemented and tested** for current simulated-screen scenarios; live terminal/provider interaction is not claimed. |

## Documentation and explicit limits

| Requirement | Evidence | Status |
| --- | --- | --- |
| Manual callback and device login remain deferred, not closure prerequisites. | Issue #164 decision; [Phase two auth boundary](phase-two-auth-boundary.md) | **Intentionally deferred.** |
| Live authorization and billable inference require explicit opt-in. | [Security notes](phase-one-security.md), [setup and usage](README.md) | **Not performed; no live-account evidence claimed.** |
| Provider process termination or a lost response may make token recovery impossible. | [Security notes](phase-one-security.md), [setup and usage](README.md) | **Documented limitation.** When KompoSyn observes an ambiguous response failure, it preserves credentials, marks reconnect required, and suppresses automatic replay. Termination before persistence may still require sign-in. |
| Native Unix and Windows file protection/locking validation. | Existing Unix/Windows build-tagged lock and ACL implementations; hosted test matrix | **Unix local tests included in this PR; Windows test evidence pending this PR's hosted CI.** Earlier hosted checks passed the existing in-process suite, which does not substitute for this new process coverage. |
| Standalone `komposyn-lg` compatibility. | Core repository CI compatibility job | **Pending this PR's hosted CI.** No downstream code or module replacement is committed. |

## Proposed final issue update after merge

After reviewing and merging this PR, complete the remaining broad validation
checkbox in section 6 of [issue #164](https://github.com/ayeshLK/komposyn/issues/164):
this PR adds separate-process lock/refresh/lifecycle evidence and maps the
remaining lifecycle/UI scenarios to concrete tests, including explicit partial
and unverified cases. First confirm this branch's Unix and Windows CI and
standalone compatibility jobs pass. Keep live authorization/billable inference
recorded as untested and manual/device login recorded as intentionally
deferred. Do not check off validation or close the issue before the PR merges.
