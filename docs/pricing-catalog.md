# Pricing catalog maintenance

The library ships a small offline public API list-price catalog at
`plugins/library/token-logger/pricing-catalog.json`. It is data, not a live
provider feed. Its coverage is deliberately narrow: exact model IDs, token
categories, and observed billing context only. Normal startup, import, and
reporting never fetch pricing pages.

## Reviewed OpenAI coverage

The catalog currently covers the public API list rates for `gpt-5-codex` and
`gpt-5.3-codex`. The maintained sources are the official [GPT-5.3-Codex model
page](https://developers.openai.com/api/docs/models/gpt-5.3-codex), [API
pricing](https://developers.openai.com/api/docs/pricing), and the [Codex model
page](https://developers.openai.com/api/docs/models/gpt-5-codex). Rates below
are USD per million tokens.

| Exact model | Billing context | Input | Cached input | Output | Other categories |
| --- | --- | ---: | ---: | ---: | --- |
| `gpt-5-codex` | No tier selector | $1.25 | $0.125 | $10.00 | Cache-write unknown |
| `gpt-5.3-codex` | Observed `service_tier=default` | $1.75 | $0.175 | $14.00 | Cache-write unknown |
| `gpt-5.3-codex` | Observed response `service_tier=priority` (Fast) | $3.50 | $0.35 | $28.00 | Cache-write unknown |

GPT-5.3-Codex has a documented 400K context window, but the cited pricing
sources do not define a separate long-context price boundary, cache-write rate,
or regional rate for this model; the catalog does not invent those selectors.
[OpenAI's Responses API reference](https://developers.openai.com/api/reference/python/resources/responses)
documents that the response reports the actual `service_tier` used. Fast
requests are returned with `service_tier=priority`, including requests that
asked for `fast`; the logger only selects the Fast rates when the response
value `priority` is present as evidence. Standard uses `default`. It does not
infer a tier from the requested tier, model, or client defaults. Current Codex
`token_usage_record` events do not include this response field, so those
imports remain unknown with `billing_context_missing` until the source format
provides observed tier evidence. Other tier values such as `flex` remain
unknown.

OpenAI's current pages do not establish the historical start date of these
GPT-5.3-Codex rates. The catalog therefore uses `2026-10-16T00:00:00Z`, the
review date, as the start of KompoSyn's supported estimate interval. This is a
conservative catalog boundary, not a claim about when OpenAI first charged
these prices; events before it remain unknown. Recheck official sources before
extending that interval or adding historical rates. Reasoning tokens are part
of output usage and are billed at the output rate; do not add a second
reasoning charge.

## Reviewed Anthropic coverage

The current matrix covers the exact Claude API model IDs `claude-opus-5-5` and
`claude-sonnet-5-5`, using Anthropic's [pricing page](https://platform.claude.com/docs/en/about-claude/pricing),
[Fast mode rates](https://platform.claude.com/docs/en/build-with-claude/fast-mode),
[Messages API usage schema](https://platform.claude.com/docs/en/api/messages/create),
and [data-residency rules](https://platform.claude.com/docs/en/manage-claude/data-residency).
All rates are USD per million tokens and are stored as full exact selector
combinations in the catalog.

| Exact model | Standard, global: input / output / cache read / 5m write / 1h write | Other supported contexts |
| --- | --- | --- |
| `claude-opus-5-5` | $4 / $20 / $0.20 / $5 / $8 | Batch: half of each rate; Fast: $8 / $40 / $0.40 / $10 / $16; `inference_geo=us`: 1.1x all categories |
| `claude-sonnet-5-5` | $2 / $10 / $0.10 / $2.50 / $4 | Batch: half of each rate; `inference_geo=us`: 1.1x all categories |

The adapter uses the response usage fields `service_tier`, `speed`, and
`inference_geo`; it does not infer them from request settings or defaults. A
rate is selected only when all three values match a reviewed catalog entry.
`priority` service-tier records and any other unlisted context remain
unpriced. Batch pricing applies the documented 50% token discount, with cache
duration and US-only multipliers composed in the catalog. Fast mode is covered
only for Opus 5.5.

Anthropic usage reports provide `cache_creation_input_tokens` as an aggregate
and, when available, `cache_creation.ephemeral_5m_input_tokens` and
`cache_creation.ephemeral_1h_input_tokens`. The logger prices cache writes
only when the duration values are present and sum to the aggregate. Aggregate-
only or incomplete splits remain unknown for the cache-write portion while
the report retains any verified subtotal. Unknown nested usage keys still
make the record unsupported and prevent safe unchanged-snapshot reuse.
`fallback_credit` remains recognized optional metadata and is neither stored
nor used to infer a discount. Reasoning-token details remain a subset of
`output_tokens`; they do not add a second output charge.

The provider pages do not establish historical start dates for this reviewed
matrix. KompoSyn begins these estimates at `2026-10-16T00:00:00Z`, a
conservative local support boundary rather than a claim about the first date
Anthropic charged these rates. Earlier events remain unknown. Other Claude
models, including Haiku 5.5's input-length pricing boundary, remain outside
this matrix until their exact applicability can be safely represented.

For each catalog change, maintainers should:

1. Verify every changed rate and billing modifier against the linked official
   provider documentation; record its URL and review date.
2. Add a new stable entry ID and per-entry pricing version for a new rate. Keep
   all immutable fields of released entries unchanged. An old entry may only be
   closed once by changing `effective_to` from null to its evidence-backed
   exclusive boundary; add the replacement with a new ID/version and matching
   `effective_from`.
3. Include only exact model identifiers, categories, and applicability the
   adapters can establish. Do not map client aliases or infer subscription,
   cloud, region, service-tier, speed, context-window, or cache-duration
   pricing from absent evidence.
4. Add or update fixtures and tests for model matching, effective boundaries,
   token categories, exact arithmetic, and the resulting provenance/reason.
5. Review the diff for currency, numerator/denominator units, rounding effects,
   and custom-rule precedence. Run the full Go test suite and catalog validation.

The numeric rate is in integer currency microunits per `denominator_tokens`.
For example, USD 1.25 per million tokens is `1250000 / 1000000` micros per
token. Calculation uses exact rational arithmetic and rounds once per event to
the nearest microunit, with ties away from zero. Catalog synchronization only
inserts missing bundled IDs and transactionally applies that one-way interval
closure after verifying all immutable fields. It rejects changed rates,
provenance, reopened/changed intervals, and never updates account- or
user-specific rules. A higher-precedence exact user rule may intentionally
customize a bundled rate.

JSON and CSV reports expose the matched model selector and effective interval
along with catalog version and source, so grouped results that combine multiple
entries visibly report `mixed` provenance instead of implying a single rule.
Grouped unknown costs retain a reason-to-count object rather than collapsing
distinct causes into one label.

Bundled values describe public API list-price equivalents, not invoices. They
exclude taxes, credits, negotiated pricing, subscription entitlements, and
provider resellers. If the source data lacks a required billing modifier, keep
the result unknown and explain the missing context instead of guessing.

Human reports translate unavailable-cost reason codes into explanatory labels
and include a bounded provider/model breakdown of unpriced requests, regardless
of the selected report grouping. JSON reason codes and CSV columns remain the
machine-readable contract. A missing public catalog match means no reviewed
bundled rate applies; it does not indicate a parser failure or justify a
fallback price. The library exposes `Store.UpsertPricing` for applications
that need custom rates; `komposyn-lg` does not currently provide a custom-rate
editor.

Human reports give reason-specific next steps. Missing model coverage calls
for checking the exact model and adding a rate only with official evidence.
An out-of-range event needs a verified rate for its event date; current prices
are not applied retroactively. A missing category rate requires verified
pricing for that category. Where selecting a rate depends on billing context,
the estimate stays unknown until the source provides enough evidence. Ambiguous
rules and currency conflicts must be resolved before estimating. The coverage
appendix wraps to the available terminal width and shows at most eight
provider/model pairs.
