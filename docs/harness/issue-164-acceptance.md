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
| Successful login validates identity, persists credentials, publishes safe completion/status, and does not choose a model or send inference. | `internal/harness/auth/openai/oauth_test.go`: `TestLoginRegistersAndStoresOnlyValidatedCandidate`; `plugins/ui/tview/issue164_acceptance_test.go`: `TestIssue164LoginSuccessAndDenialThroughRuntimeAdapterAndScreen`, `TestIssue164AdapterForwardsLoginCompletion` | **Implemented and tested** using real manager identity tests plus a fake coordinator through runtime, UI adapter, and simulated screen. The screen flow asserts persisted profile, no selected model, and no inference. |
| Denied/canceled login and identity login without inference permission remain distinct and preserve a previously valid profile. | `oauth_test.go`: `TestLoginCancellationAndWrongStateDoNotPersistCandidate`; `plugins/ui/tview/issue164_acceptance_test.go`: `TestIssue164LoginSuccessAndDenialThroughRuntimeAdapterAndScreen`; `runtime/chat_test.go`: `TestOAuthIdentityWithoutInferencePermissionCannotStartChat` | **Implemented and tested.** Denial preserves an existing profile in the manager test and renders a distinct screen error; identity-only login displays missing permission and cannot start inference. |
| Reconnect targets the intended registration; model/account changes retain existing context-reset semantics. | `auth/store_test.go`: `TestOAuthLifecycleGenerationRejectsStaleReconnects`; `runtime/chat_test.go`: `TestOAuthAccessTokenRefreshKeepsSelectedModelAndChatContext`, `TestTemporaryMultiMessageChatAndConfirmedModelReset`; `plugins/ui/tview/issue164_acceptance_test.go`: reconnect case in `TestIssue164ReturningLoginAndStaleCompletionKeepTheRightView` | **Implemented and tested.** Returning login updates the same profile while preserving account identity, selected model, and temporary history. |
| Disconnect retains the registration while clearing local tokens; removal deletes it; remote revocation uncertainty is reported accurately. | `oauth_test.go`: `TestDisconnectReportsRemoteRevocationAndRemovesLocalTokens`; `plugins/ui/tview/issue164_acceptance_test.go`: `TestIssue164ConfirmedDisconnectAndRemoveThroughUIAdapter`; `auth/store_process_test.go`: `TestOAuthLifecycleMutationWinsAcrossProcesses` | **Implemented and tested** through the runtime/UI adapter for confirmation and local result distinctions, and in the provider manager for remote revocation reporting. |
| Cancel during auth/store waits, suppress stale UI updates, and keep shutdown bounded. | `runtime/chat_test.go`: `TestSnapshotAndCancelStayResponsiveDuringAuthStoreLockWait`, `TestCanceledChatRefreshDoesNotStartInferenceOrAppendTurn`; `plugins/ui/tview/ui_test.go`: stop-drain and cancellation cases; `plugins/ui/tview/issue164_acceptance_test.go`: late-completion case in `TestIssue164ReturningLoginAndStaleCompletionKeepTheRightView` | **Implemented and tested.** The simulated screen cancels login, then receives a deliberately late result and confirms the picker remains current. |
| Explicit API-key and environment-reference profiles for supported providers never fall back to another credential. | `auth/registry_test.go`: `TestExplicitEnvironmentProfilesResolveOnlyForTheirProvider`, `TestMissingExplicitEnvironmentCredentialIsConfigurationFailure`; `model/catalog_test.go`: `TestDiscoveryKeepsProfilesSeparateAndPartialFailuresVisible`; `plugins/ui/tview/issue164_acceptance_test.go`: `TestIssue164SelectedCredentialFailureDoesNotFallback` | **Implemented and tested** for both-provider environment routing, partial discovery, selected OAuth failure reporting, and zero fallback inference through an available API-key profile. |
| Public status, progress, snapshots, errors, and browser capability do not expose secrets or authorization URL hints; Manage usage invokes the injected opener. | `oauth_test.go`: URL-free progress, sanitized browser/open-usage failures; `auth/registry_test.go`: typed sanitized errors; `runtime/chat_test.go`: sanitized request failure; PR #166 regression `TestOpenUsageUsesInjectedBrowserAndSanitizesErrors` | **Implemented and tested** for these public output boundaries. |
| `/model` navigation preserves draft/focus and responsive progress/cancel behavior. | `plugins/ui/tview/ui_test.go`: `TestPickerInteractionOnSimulationScreen`, `TestEscapeFromModelFilterReturnsToChatAndPreservesSelection`, cancellation and stop-drain cases | **Implemented and tested** for current simulated-screen scenarios; live terminal/provider interaction is not claimed. |

## Documentation and explicit limits

| Requirement | Evidence | Status |
| --- | --- | --- |
| Manual callback and device login remain deferred, not closure prerequisites. | Issue #164 decision; [Phase two auth boundary](phase-two-auth-boundary.md) | **Intentionally deferred.** |
| Live authorization and billable inference require explicit opt-in. | [Security notes](phase-one-security.md), [setup and usage](README.md) | **Not performed; no live-account evidence claimed.** |
| Provider process termination or a lost response may make token recovery impossible. | [Security notes](phase-one-security.md), [setup and usage](README.md) | **Documented limitation.** When KompoSyn observes an ambiguous response failure, it preserves credentials, marks reconnect required, and suppresses automatic replay. Termination before persistence may still require sign-in. |
| Native Unix and Windows file protection/locking validation. | Existing Unix/Windows build-tagged lock and ACL implementations; hosted test matrix | **Locally tested on Unix; hosted Windows process and native lock tests passed on the reviewed PR run.** The follow-up commit's hosted run is pending. |
| Standalone `komposyn-lg` compatibility. | Core repository CI compatibility job | **Passed on the reviewed PR run; follow-up commit's hosted run is pending.** No downstream code or module replacement is committed. |

## Proposed final issue update after merge

After review and merge, the maintainer can complete the remaining broad
validation checkbox in section 6 of [issue #164](https://github.com/ayeshLK/komposyn/issues/164):
this PR supplies separate-process reliability/lifecycle tests and simulated
screen/runtime/UI-adapter evidence for the previously missing acceptance
scenarios. Reconfirm that this follow-up commit's Unix and Windows CI, race,
release candidate, and standalone compatibility jobs pass before checking it
off. Keep live authorization/billable inference recorded as untested and
manual/device login recorded as intentionally deferred. Do not check off
validation or close the issue before the PR merges.
