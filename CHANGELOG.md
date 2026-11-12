# Changelog

All notable changes to KompoSyn are documented in this file. Release entries
are prepared from Conventional Commits by Release Please and reviewed before
publication.

## [0.15.0](https://github.com/ayeshLK/komposyn/compare/v0.14.0...v0.15.0) (2026-11-11)


### Features

* **harness:** improve temporary chat file reads ([#284](https://github.com/ayeshLK/komposyn/issues/284)) ([eb9357d](https://github.com/ayeshLK/komposyn/commit/eb9357d70f1974d4447b4f435905270b7f2a98ff))


### Bug Fixes

* update Go toolchain to 1.26.11 ([#283](https://github.com/ayeshLK/komposyn/issues/283)) ([e0f3820](https://github.com/ayeshLK/komposyn/commit/e0f3820b04e3d2a79ee31c076612afbaf5f49f1e))

## [0.14.0](https://github.com/ayeshLK/komposyn/compare/v0.13.1...v0.14.0) (2026-11-10)


### Experimental release highlights

This release adds conversation-local plans, clarification, and temporary tool
permissions on OpenAI API-key Responses and Anthropic API-key Messages routes.
Continue with ChatGPT remains a separate, text-only authorization route;
discovered models do not guarantee chat or tool compatibility. Terminal updates
include multiline draft paste, transcript selection/copy, and responsive usage
layouts.

Linux exact-file reads require trusted Bubblewrap inspection, an explicit
lookup root, and once-only approval naming the exact target and provider/model/
profile destination. The worker is bounded to 64 KiB; unsafe, protected,
truncated, invalid-text, canceled, and stale results are refused or withheld.
Unregistered file contents may be sensitive and approved contents may recur in
the valid conversation. Concurrent renames do not preserve strict live pathname
containment. Native evidence is limited to recorded Linux amd64/Bubblewrap 0.9.0
profiles; it does not establish cross-platform filesystem enforcement.

**Live OpenAI API-key, Anthropic API-key, and ChatGPT login/inference validation
is pending until after publication.** Automated fixture results do not verify
account eligibility, model access, or live service behavior. Authorized account
operators will validate the published binaries separately.

This is also a Token Logger core release: additive Codex recovery preview,
transactional apply, and committed-history APIs use SQLite schema **19**.
Opening a schema-18 database migrates it transactionally after a verified online
backup; no automatic down-migration exists. Retain that backup and do not use
an older core against the upgraded database. Event schema and standalone
protocol-v1 semantics remain unchanged. `komposyn-lg` stays independently
versioned and is not distributed here.

Chat state and tool permissions remain in memory. Credentials persist as
plaintext under private file permissions/ACLs. Mutation, shell execution,
web/image tools, persistent conversations/tasks, MCP, and general plugin loading
remain unavailable. See the [five-minute walkthrough](https://github.com/ayeshLK/komposyn/blob/v0.14.0/docs/harness/first-use.md)
and [release overview](https://github.com/ayeshLK/komposyn/blob/v0.14.0/docs/harness/next-release-overview.md)
for setup, capability limits, and sanitized feedback/private security routes.


### Features

* add provider tool protocol support ([#204](https://github.com/ayeshLK/komposyn/issues/204)) ([9e431c9](https://github.com/ayeshLK/komposyn/commit/9e431c9b0331c783f53101f7d78b852b3e682748))
* **harness:** add bounded tool runtime loop ([#206](https://github.com/ayeshLK/komposyn/issues/206)) ([6f6425d](https://github.com/ayeshLK/komposyn/commit/6f6425df44503109964ecb5053a2bb8666b55e75))
* **harness:** add checked read authority lifecycle ([#247](https://github.com/ayeshLK/komposyn/issues/247)) ([5595546](https://github.com/ayeshLK/komposyn/commit/5595546c10b2c8228e31b09cd498cedd44904e2e))
* **harness:** add conversation-local plan and input tools ([#211](https://github.com/ayeshLK/komposyn/issues/211)) ([67da3a1](https://github.com/ayeshLK/komposyn/commit/67da3a13f8fb5c8221ef8bfdb2a0c8ac6ac3a048))
* **harness:** add filesystem enforcement foundation ([#212](https://github.com/ayeshLK/komposyn/issues/212)) ([856beb9](https://github.com/ayeshLK/komposyn/commit/856beb9f5e90dfb57ec10278126495edf3ec09a3))
* **harness:** add in-memory filesystem root lifecycle ([#246](https://github.com/ayeshLK/komposyn/issues/246)) ([9f1391d](https://github.com/ayeshLK/komposyn/commit/9f1391dc3f74ec959cf10885c32b52095dd98b16))
* **harness:** add retained read admission barriers ([#248](https://github.com/ayeshLK/komposyn/issues/248)) ([4a5c389](https://github.com/ayeshLK/komposyn/commit/4a5c389295c0a830af46df6aee1f39982051a786))
* **harness:** add tool approval and permissions UI ([#210](https://github.com/ayeshLK/komposyn/issues/210)) ([e42b186](https://github.com/ayeshLK/komposyn/commit/e42b186f114d03a49d83d8e4e868584a763c90a0))
* **harness:** add tool authorization foundation ([#202](https://github.com/ayeshLK/komposyn/issues/202)) ([acbe54c](https://github.com/ayeshLK/komposyn/commit/acbe54c6c81af456756d14c3235c3d0aaafd1496))
* **harness:** enable approved exact-file reads ([#264](https://github.com/ayeshLK/komposyn/issues/264)) ([f629cc8](https://github.com/ayeshLK/komposyn/commit/f629cc87ce4bd9344299257b51c70877e7c29d03))
* **readbrokerstudy:** support Bubblewrap 0.9 snapshots ([#221](https://github.com/ayeshLK/komposyn/issues/221)) ([5fb44dc](https://github.com/ayeshLK/komposyn/commit/5fb44dc7e0c161d6010628eb4017d5c88b33e097))
* **recovery:** add bounded Codex preview ([#208](https://github.com/ayeshLK/komposyn/issues/208)) ([607b8a2](https://github.com/ayeshLK/komposyn/commit/607b8a20391f998c2769b9fbf332ff7f3bfb20d1))
* **recovery:** apply reviewed Codex plans transactionally ([#209](https://github.com/ayeshLK/komposyn/issues/209)) ([924c9d4](https://github.com/ayeshLK/komposyn/commit/924c9d499f776366e8dfe89cf81480bfe358be57))
* **recovery:** inspect committed allocation history ([#214](https://github.com/ayeshLK/komposyn/issues/214)) ([c6d60e4](https://github.com/ayeshLK/komposyn/commit/c6d60e4522581a025fb7708c8288d170f0af8dbe))
* **research:** prototype SQLite writable object barrier ([#239](https://github.com/ayeshLK/komposyn/issues/239)) ([4bb13c6](https://github.com/ayeshLK/komposyn/commit/4bb13c6e98a956b068268adc9bbc78fb9832ecdf))
* **sandbox-study:** support verified Bubblewrap compatibility ([#220](https://github.com/ayeshLK/komposyn/issues/220)) ([c95affd](https://github.com/ayeshLK/komposyn/commit/c95affd3ba96d7fa294266dad312426f3d5374ac))
* **storage:** add effective recovery allocations ([#207](https://github.com/ayeshLK/komposyn/issues/207)) ([b3dbc7f](https://github.com/ayeshLK/komposyn/commit/b3dbc7f0eccead397444f6ce1d094ddfaf55d06c))


### Bug Fixes

* **codex:** accept optional reasoning metadata ([#203](https://github.com/ayeshLK/komposyn/issues/203)) ([370892c](https://github.com/ayeshLK/komposyn/commit/370892cf7af3745d9992e22e66d07b001cd9101c))
* **codex:** ignore token counts without usage info ([#201](https://github.com/ayeshLK/komposyn/issues/201)) ([e3fbf99](https://github.com/ayeshLK/komposyn/commit/e3fbf998efa355253a9d29fa94df66a7ab05fae7))
* **harness:** classify real-store read controls ([#234](https://github.com/ayeshLK/komposyn/issues/234)) ([2eb062a](https://github.com/ayeshLK/komposyn/commit/2eb062a524701bb8fcfa1dbe6d0d8a81eae8e28d))
* **harness:** classify revoked read continuation as interrupted ([#266](https://github.com/ayeshLK/komposyn/issues/266)) ([83d5a47](https://github.com/ayeshLK/komposyn/commit/83d5a475c13ec6e5bfb427d2eadcefb70a40174e))
* **harness:** fit Stage 6 binary within worker bound ([#262](https://github.com/ayeshLK/komposyn/issues/262)) ([9a36a76](https://github.com/ayeshLK/komposyn/commit/9a36a764a6f3d49e9aad3d1c661ee061ca5af2e5))
* **readbrokerstudy:** address native actor review findings ([13e04dd](https://github.com/ayeshLK/komposyn/commit/13e04ddd4ee68991ebc31cff8522d52b4b990c90))
* **readbrokerstudy:** bound fixture session cleanup ([770ee54](https://github.com/ayeshLK/komposyn/commit/770ee549f12dbb4f959a6de02ceda2a378d3ec69))
* **readbrokerstudy:** omit unavailable generation evidence ([00dc9b4](https://github.com/ayeshLK/komposyn/commit/00dc9b488f5272b4ca867e3b28fa2611fcd8e6ef))
* **readbrokerstudy:** refuse reads under mutable protected locations ([#226](https://github.com/ayeshLK/komposyn/issues/226)) ([d2686a8](https://github.com/ayeshLK/komposyn/commit/d2686a80d406c42fbb1e6e7a4d10e05e2c4005e1))
* **readbrokerstudy:** repair native baseline failures ([#223](https://github.com/ayeshLK/komposyn/issues/223)) ([c5c6297](https://github.com/ayeshLK/komposyn/commit/c5c629738371c969b0c575259dff4a1311988a68))

## [0.13.1](https://github.com/ayeshLK/komposyn/compare/v0.13.0...v0.13.1) (2026-10-31)


### Bug Fixes

* **chat:** retain subscription stream output and add copy controls ([#192](https://github.com/ayeshLK/komposyn/issues/192)) ([381d4d3](https://github.com/ayeshLK/komposyn/commit/381d4d3609fd6d972c7443d4a2b757ad18ba19df))

## [0.13.0](https://github.com/ayeshLK/komposyn/compare/v0.12.0...v0.13.0) (2026-10-30)


### Features

* **ui:** recall submitted questions with arrow keys ([#189](https://github.com/ayeshLK/komposyn/issues/189)) ([d2255af](https://github.com/ayeshLK/komposyn/commit/d2255af0f7b79302edba82d98673113011d8dd09))


### Bug Fixes

* **ui:** improve chat turn spacing and reply contrast ([#188](https://github.com/ayeshLK/komposyn/issues/188)) ([8216a6f](https://github.com/ayeshLK/komposyn/commit/8216a6fcb0b1859359791e473e9ada9b219581e8))

## [0.12.0](https://github.com/ayeshLK/komposyn/compare/v0.11.0...v0.12.0) (2026-10-30)


### Features

* **chat:** expose bounded rejection diagnostics ([#186](https://github.com/ayeshLK/komposyn/issues/186)) ([f7f10f0](https://github.com/ayeshLK/komposyn/commit/f7f10f0430ef15977e798ebcbf2bcc0fc21d9ddc))


### Bug Fixes

* **chat:** improve continuation and usage UX ([e68a8e9](https://github.com/ayeshLK/komposyn/commit/e68a8e99f29371312a1e4f593f9e2c5225104c65))
* **chat:** improve continuation and usage UX ([10107d4](https://github.com/ayeshLK/komposyn/commit/10107d4b2d68cc80960ee88ec5b3722aa3d2841b))
* **chat:** page usage rows in wide terminals ([0f6c57a](https://github.com/ayeshLK/komposyn/commit/0f6c57a0a7aaa063c59e77be646184d8dfb439d3))
* **chat:** scroll long usage details ([a6cea7b](https://github.com/ayeshLK/komposyn/commit/a6cea7b72eed138536bfc7f245a47ef8eb350186))

## [0.11.0](https://github.com/ayeshLK/komposyn/compare/v0.10.0...v0.11.0) (2026-10-30)


### Features

* **ui:** improve chat navigation and usage display ([#182](https://github.com/ayeshLK/komposyn/issues/182)) ([ef63d18](https://github.com/ayeshLK/komposyn/commit/ef63d189f4858018f54ebf369365bee903f1f766))


### Bug Fixes

* **ci:** allow release jobs to inspect draft releases ([#179](https://github.com/ayeshLK/komposyn/issues/179)) ([8afe570](https://github.com/ayeshLK/komposyn/commit/8afe570347a061f9715d924eb88e897ff587f6a0))
* **ci:** resolve draft releases during binary validation ([#180](https://github.com/ayeshLK/komposyn/issues/180)) ([5806431](https://github.com/ayeshLK/komposyn/commit/5806431ec2d08b313f3422f0706918e357c39c60))

## [0.10.0](https://github.com/ayeshLK/komposyn/compare/v0.9.0...v0.10.0) (2026-10-29)


### Features

* **auth:** add provider-scoped auth registry and interactions ([1c36607](https://github.com/ayeshLK/komposyn/commit/1c36607463b160afee8bcbae3077a4ea3971ae1c))
* **auth:** add provider-scoped auth registry and interactions ([82ca93a](https://github.com/ayeshLK/komposyn/commit/82ca93a1dc1956097a90d8133c48bde956e6f102))
* **diagnostics:** add contract v1 foundation ([9fa3baa](https://github.com/ayeshLK/komposyn/commit/9fa3baabe866ff70ed237e10bbdb88bb6d7eee16))
* **diagnostics:** add contract v1 foundation ([e61d434](https://github.com/ayeshLK/komposyn/commit/e61d43400f90b8335d6f15b3ad6a29851d202730))
* **diagnostics:** persist production evidence ([#172](https://github.com/ayeshLK/komposyn/issues/172)) ([fa67515](https://github.com/ayeshLK/komposyn/commit/fa67515efe7430da47c1819cba4a3cba0445ea40))
* **harness:** add ChatGPT OAuth sign-in ([#161](https://github.com/ayeshLK/komposyn/issues/161)) ([bd9f3b1](https://github.com/ayeshLK/komposyn/commit/bd9f3b13dd4dadabd7057e921b7190c3a2ddb3b2))
* **harness:** add experimental temporary chat ([#157](https://github.com/ayeshLK/komposyn/issues/157)) ([e006cad](https://github.com/ayeshLK/komposyn/commit/e006cad6492ac21f654c877bd43e055746aa3bce))
* **harness:** prepare signed CLI release workflow ([#160](https://github.com/ayeshLK/komposyn/issues/160)) ([bb91840](https://github.com/ayeshLK/komposyn/commit/bb91840b9df42e97f12ff3a0d2624b999e8cadba))
* instrument structured diagnostic boundaries ([#170](https://github.com/ayeshLK/komposyn/issues/170)) ([2d12dc7](https://github.com/ayeshLK/komposyn/commit/2d12dc77d95db48207cd7e50712ee967295b0c81))
* persist diagnostic evidence and add inspection APIs ([#169](https://github.com/ayeshLK/komposyn/issues/169)) ([fc63c3c](https://github.com/ayeshLK/komposyn/commit/fc63c3cba307984afa2862198785dc315647f26d))


### Bug Fixes

* **auth:** route usage page through browser opener ([d2c1ba2](https://github.com/ayeshLK/komposyn/commit/d2c1ba27a5e21a3163ce5f2cdafe47dd927d05ae))
* **diagnostics:** address foundation review feedback ([395cc8c](https://github.com/ayeshLK/komposyn/commit/395cc8c165f3cb5702790f6ca89dedef8388e1e6))
* **harness:** make OAuth refresh survive cancellation ([#165](https://github.com/ayeshLK/komposyn/issues/165)) ([1232f87](https://github.com/ayeshLK/komposyn/commit/1232f875acd9c2f7fe742fe19f3b43ae07b856a3))

## [0.9.0](https://github.com/ayeshLK/komposyn/compare/v0.8.0...v0.9.0) (2026-10-21)


### Features

* add read-only project inspection ([b257175](https://github.com/ayeshLK/komposyn/commit/b25717555cd462ba3e86633edb35dcc2ac112d56))
* **identity:** add read-only project inspection evidence ([732b497](https://github.com/ayeshLK/komposyn/commit/732b49756f5872bb563ff28696055df0e08d9d73))


### Bug Fixes

* **core:** bound and sanitize project inspection ([167cebf](https://github.com/ayeshLK/komposyn/commit/167cebfc4239ecc3fbfd869c272db3ebb9a48b89))
* **core:** close direct import classification gaps ([1471b6a](https://github.com/ayeshLK/komposyn/commit/1471b6ab7fad85f875e2dd36a53f5da0c02153e8))
* **core:** keep Claude CWD name evidence paired ([334ceda](https://github.com/ayeshLK/komposyn/commit/334ceda1c7762c72da1d5d75eb03b5d2b719c300))
* **identity:** preserve trusted Claude project name provenance ([6028a7a](https://github.com/ayeshLK/komposyn/commit/6028a7a68abc604920904bcfc9d1612e8baf5e6a))
* **ingestion:** require positive adapter evidence ([4718aea](https://github.com/ayeshLK/komposyn/commit/4718aea0662b123d572a73bc02fec6a43a9a795a))
* **ingestion:** require positive adapter evidence ([d7625a5](https://github.com/ayeshLK/komposyn/commit/d7625a57d22135adcca3b409bcbf3f938e1a0a35))
* sanitize project inspection privacy output ([#154](https://github.com/ayeshLK/komposyn/issues/154)) ([e8a4288](https://github.com/ayeshLK/komposyn/commit/e8a4288f8a2c0e6262bf7c19936d61e956e78081))

## [0.8.0](https://github.com/ayeshLK/komposyn/compare/v0.7.1...v0.8.0) (2026-10-19)


### Features

* **core:** add validated append checkpoints for Claude and Codex ([#145](https://github.com/ayeshLK/komposyn/issues/145)) ([e5d396a](https://github.com/ayeshLK/komposyn/commit/e5d396a61a6222610d3f8e43772d5861692a01a9))

## [0.7.1](https://github.com/ayeshLK/komposyn/compare/v0.7.0...v0.7.1) (2026-10-19)


### Performance Improvements

* eliminate redundant collector snapshot acquisition ([#141](https://github.com/ayeshLK/komposyn/issues/141)) ([36fea98](https://github.com/ayeshLK/komposyn/commit/36fea986b18d2c5ad32f4876b50412a03f9d933f))
* stream collector source verification ([#143](https://github.com/ayeshLK/komposyn/issues/143)) ([6b69e02](https://github.com/ayeshLK/komposyn/commit/6b69e02d3162ea5cf4971ca28403952f4e1ae291))

## [0.7.0](https://github.com/ayeshLK/komposyn/compare/v0.6.0...v0.7.0) (2026-10-17)


### Features

* **cli:** add public help and shell completion ([10dcda8](https://github.com/ayeshLK/komposyn/commit/10dcda83f817c7886d4e0592747bee2b17ba90b6))
* **cli:** add public help and shell completion ([7a5d5fd](https://github.com/ayeshLK/komposyn/commit/7a5d5fdf15719fbf54b1e4e77e3904938bae7067)), closes [#110](https://github.com/ayeshLK/komposyn/issues/110)
* prepare final combined release for issue [#46](https://github.com/ayeshLK/komposyn/issues/46) ([#126](https://github.com/ayeshLK/komposyn/issues/126)) ([43cb710](https://github.com/ayeshLK/komposyn/commit/43cb71014680abbe57011885a150604eef6b93a6))


### Bug Fixes

* **cli:** complete command and help targets accurately ([2990cdb](https://github.com/ayeshLK/komposyn/commit/2990cdb530bf2489365150511992941cebe5caa8))
* **cli:** retain PowerShell options after values ([c3e334b](https://github.com/ayeshLK/komposyn/commit/c3e334b0fac8b6956796e3e04d10ae2ed4a0001b))
* **cli:** scope shell completions to active command ([573e26f](https://github.com/ayeshLK/komposyn/commit/573e26fc6b524cbdf4fc8da97204603a40f46eef))
* recover private schema inspection journals ([#127](https://github.com/ayeshLK/komposyn/issues/127)) ([0fd70e0](https://github.com/ayeshLK/komposyn/commit/0fd70e051268f7790cddf484ac969f77bf571953))

## [0.6.0](https://github.com/ayeshLK/komposyn/compare/v0.5.0...v0.6.0) (2026-10-16)


### Features

* **repl:** make pricing coverage opt-in ([#123](https://github.com/ayeshLK/komposyn/issues/123)) ([51e5bee](https://github.com/ayeshLK/komposyn/commit/51e5bee7619826ab115b16623e878092690a33a1))

## [0.5.0](https://github.com/ayeshLK/komposyn/compare/v0.4.0...v0.5.0) (2026-10-16)


### Features

* explain unavailable pricing in reports ([#103](https://github.com/ayeshLK/komposyn/issues/103)) ([1a11e49](https://github.com/ayeshLK/komposyn/commit/1a11e49fa6cc53e5e06e052344a33d0ffc2f85c5))
* **pricing:** add evidence-based Anthropic coverage ([#106](https://github.com/ayeshLK/komposyn/issues/106)) ([8be83db](https://github.com/ayeshLK/komposyn/commit/8be83db949cfe89023bfbd7089a795685991688d))
* **pricing:** add GPT-5.3 Codex API coverage ([#105](https://github.com/ayeshLK/komposyn/issues/105)) ([e7c8946](https://github.com/ayeshLK/komposyn/commit/e7c89467f4f87d11a35046d9df59eea7efd10241))
* **pricing:** model billing context selectors ([#104](https://github.com/ayeshLK/komposyn/issues/104)) ([44e9add](https://github.com/ayeshLK/komposyn/commit/44e9add3231dbc82c3b9e6aa9579d469c932709c))

## [0.4.0](https://github.com/ayeshLK/komposyn/compare/v0.3.0...v0.4.0) (2026-10-16)


### Features

* add persistent multi-source background watch ([#95](https://github.com/ayeshLK/komposyn/issues/95)) ([c3cf5ab](https://github.com/ayeshLK/komposyn/commit/c3cf5abc24ea7c6872c644c202e460f1f1bd64ff))


### Bug Fixes

* accept Claude fallback credit metadata ([#99](https://github.com/ayeshLK/komposyn/issues/99)) ([75dfb21](https://github.com/ayeshLK/komposyn/commit/75dfb21cc4d43dc1e07b3941e23e264c5898f67f))
* **token-logger:** compact resource token counts ([#97](https://github.com/ayeshLK/komposyn/issues/97)) ([42a5d6e](https://github.com/ayeshLK/komposyn/commit/42a5d6e0554d2045d70a5bbd71d2fed722d5a809))

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
