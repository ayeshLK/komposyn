# Changelog

All notable changes to KompoSyn are documented in this file. Release entries
are prepared from Conventional Commits by Release Please and reviewed before
publication.

## Unreleased

### Bug Fixes

* accept Claude `fallback_credit` usage metadata and automatically recover
  previously skipped events from unchanged transcripts

## [0.3.0](https://github.com/ayeshLK/komposyn/compare/v0.2.2...v0.3.0) (2026-10-15)


### Features

* reconcile projects and add hierarchical navigation ([#88](https://github.com/ayeshLK/komposyn/issues/88)) ([435ebe2](https://github.com/ayeshLK/komposyn/commit/435ebe2fc70fabe3967e56e718ccf8d7504b7354))


### Bug Fixes

* restore watch and REPL collection correctness ([#91](https://github.com/ayeshLK/komposyn/issues/91)) ([1aa30f9](https://github.com/ayeshLK/komposyn/commit/1aa30f9712ac62c4d636183ab5ab633fe54408cd))

## [0.2.2](https://github.com/ayeshLK/komposyn/compare/v0.2.1...v0.2.2) (2026-10-15)


### Bug Fixes

* keep interactive table rows aligned and adapt to terminal width ([#86](https://github.com/ayeshLK/komposyn/issues/86)) ([4fc65cc](https://github.com/ayeshLK/komposyn/commit/4fc65cc7d7411eed9e8ae21a02ca86af2d2ab0f6))

## [0.2.1](https://github.com/ayeshLK/komposyn/compare/v0.2.0...v0.2.1) (2026-10-14)


### Bug Fixes

* accept Codex cache_write_input_tokens alias and derive project id from cwd ([#83](https://github.com/ayeshLK/komposyn/issues/83)) ([29dbc53](https://github.com/ayeshLK/komposyn/commit/29dbc53557ee76b949839c4467a6afffd48c94d5))

## [0.2.0](https://github.com/ayeshLK/komposyn/compare/v0.1.0...v0.2.0) (2026-10-14)


### Features

* add interactive terminal editing and /clear ([#66](https://github.com/ayeshLK/komposyn/issues/66)) ([7720965](https://github.com/ayeshLK/komposyn/commit/77209659dd617356784aa477e7a3936207517c8f))
* add versioned public pricing catalog ([#75](https://github.com/ayeshLK/komposyn/issues/75)) ([314a106](https://github.com/ayeshLK/komposyn/commit/314a106c6a908d866078c9992a01fa11161f114f))
* define client-neutral import outcomes ([#61](https://github.com/ayeshLK/komposyn/issues/61)) ([e924425](https://github.com/ayeshLK/komposyn/commit/e924425d0a0c1fa62812692145d73c1463e049e0))
* improve human-readable usage reports ([#60](https://github.com/ayeshLK/komposyn/issues/60)) ([b848dce](https://github.com/ayeshLK/komposyn/commit/b848dce0f447d329ad5c8d97aa49c6dbd07f1f4e))


### Bug Fixes

* import Codex token usage records without double-counting ([#62](https://github.com/ayeshLK/komposyn/issues/62)) ([62e5d37](https://github.com/ayeshLK/komposyn/commit/62e5d37743b1fa4202a0358493940608fe6feea3))
* refresh interactive report time ranges ([#71](https://github.com/ayeshLK/komposyn/issues/71)) ([26b9be2](https://github.com/ayeshLK/komposyn/commit/26b9be27d0f22fd24550f0e5484deb38dcf0e9c0))
* resolve token logger data directory consistently ([#72](https://github.com/ayeshLK/komposyn/issues/72)) ([1ce8460](https://github.com/ayeshLK/komposyn/commit/1ce846018e80ae3bb662b30213ea88c9bb8fec8e))

## 0.1.0 (2026-10-11)


### Features

* add Claude Code transcript adapter ([c8b65fc](https://github.com/ayeshLK/komposyn/commit/c8b65fcf87a093fd894b78c0f8b7ce77697c16eb))
* add CLI version and upgrade guidance ([2ced007](https://github.com/ayeshLK/komposyn/commit/2ced007fe190849d60d62f9ff9f17ae9748365e0))
* add CLI version and upgrade guidance ([#2](https://github.com/ayeshLK/komposyn/issues/2)) ([cc80fee](https://github.com/ayeshLK/komposyn/commit/cc80feec0013f57a0fa0754bbb124c91c9ca41ec))
* add explicit collector lifecycle and settings ([4a2ac6e](https://github.com/ayeshLK/komposyn/commit/4a2ac6e6e1b3f9e3be8e5b77235ccf11a7adeda1))
* add harness stdio protocol ([#3](https://github.com/ayeshLK/komposyn/issues/3)) ([d30a037](https://github.com/ayeshLK/komposyn/commit/d30a037bdb9ce6aefbe9453406f951948e17236b))
* add local diagnostic report export ([#40](https://github.com/ayeshLK/komposyn/issues/40)) ([c7c1e76](https://github.com/ayeshLK/komposyn/commit/c7c1e7612842da97c1b8a56b4c4021608e11c252))
* add release distribution workflow ([afbeb2a](https://github.com/ayeshLK/komposyn/commit/afbeb2ab037d9c0415504c6569a6f0e91394e512))
* add release distribution workflow ([#5](https://github.com/ayeshLK/komposyn/issues/5)) ([31d9029](https://github.com/ayeshLK/komposyn/commit/31d90294c5c755940b1840e3f963be2d1acba6ec))
* add token logger M1 through M4 ([cadbabc](https://github.com/ayeshLK/komposyn/commit/cadbabca658670d2d69b15abead4abad8913f587))
* promote M7 harness protocol to master ([#4](https://github.com/ayeshLK/komposyn/issues/4)) ([5a5fa50](https://github.com/ayeshLK/komposyn/commit/5a5fa50cc1516f6173d53a79d7527e74cc0974d9))


### Bug Fixes

* add report timezone option ([#36](https://github.com/ayeshLK/komposyn/issues/36)) ([82facdd](https://github.com/ayeshLK/komposyn/commit/82facddcff0015dcfdaf87bbe2b5375d81a7aafa))
* address release review findings ([df6947a](https://github.com/ayeshLK/komposyn/commit/df6947a024fff1ea5f7af558aafa44ddeac404d3))
* atomically persist adapter ingestion ([#37](https://github.com/ayeshLK/komposyn/issues/37)) ([5539e97](https://github.com/ayeshLK/komposyn/commit/5539e97071c918ef3315a21d53205c6ca7f3326e))
* atomically protect settings writes ([0280c72](https://github.com/ayeshLK/komposyn/commit/0280c721bd2b2b913b19d55cafc547167669daf4))
* atomically protect settings writes ([0f7f619](https://github.com/ayeshLK/komposyn/commit/0f7f619e44e0000887269f257f0abea75ee75d42))
* avoid duplicate collector startup poll ([4fc3e2f](https://github.com/ayeshLK/komposyn/commit/4fc3e2f2bd7575a8b50935a78391d1a613191c22))
* avoid duplicate collector startup poll ([5a31005](https://github.com/ayeshLK/komposyn/commit/5a310056d2b1c596e24227a5beefd34208c8c586))
* bucket reports by local civil hour ([f0b20aa](https://github.com/ayeshLK/komposyn/commit/f0b20aa278b759fa9e8a4b358f69c71993c35f15))
* bucket reports by local civil hour ([3ff52ae](https://github.com/ayeshLK/komposyn/commit/3ff52aed6dc1439f351d0aefd4c03aacb2861950))
* complete settings replacement atomically ([90783f2](https://github.com/ayeshLK/komposyn/commit/90783f2ca5a8396263356145fd124c64bd7be869))
* detect transcript source replacement ([#19](https://github.com/ayeshLK/komposyn/issues/19)) ([31fdb25](https://github.com/ayeshLK/komposyn/commit/31fdb258edf529860c55bbc85f6c3e95a75d89b3))
* gate release publication on installer checks ([a6a974f](https://github.com/ayeshLK/komposyn/commit/a6a974f2d654c7406e027af76fa6c6c929a2a07c))
* gate release publication on installer checks ([8fd57ba](https://github.com/ayeshLK/komposyn/commit/8fd57ba4cec6de9ecfa63bda29053c5cc2f7145b))
* normalize stored timestamps ([d59417d](https://github.com/ayeshLK/komposyn/commit/d59417d83028105b1843274e094353484d6ed66c))
* normalize stored timestamps ([223b4ff](https://github.com/ayeshLK/komposyn/commit/223b4ff9075b501dd512ee92849b0ad20f7df274))
* persist harness project and session references ([1313f39](https://github.com/ayeshLK/komposyn/commit/1313f3901d034dbe4689cab8221e66cee387f60d))
* persist harness project and session references ([5c64978](https://github.com/ayeshLK/komposyn/commit/5c64978c431faa3ead94f53ed0e65069f8a05c5e))
* persist harness references atomically ([1dac182](https://github.com/ayeshLK/komposyn/commit/1dac182120fee7658f4ea401889d96bf6b346198))
* preserve caller files in release output ([e975692](https://github.com/ayeshLK/komposyn/commit/e9756926cd8dc560088aa2e8026df510eb6166b6))
* require empty release output directory ([de36750](https://github.com/ayeshLK/komposyn/commit/de367501f3bb0894a8dd3cc4d97cec8aa56dfb16))
* scope release workflow permissions ([8d1f1b3](https://github.com/ayeshLK/komposyn/commit/8d1f1b33fcf2c8bc485a7e7b17d1174116f8434b))
* stop collector before starting ticker ([b1beaf2](https://github.com/ayeshLK/komposyn/commit/b1beaf2a4e955d493930f05b1114d40b10a974f8))

## 0.0.0

Unreleased development baseline. Changes since this baseline will be moved
into the first generated release section by Release Please.
