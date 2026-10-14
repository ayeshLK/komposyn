# Pricing catalog maintenance

The library ships a small offline public API list-price catalog at
`plugins/library/token-logger/pricing-catalog.json`. It is data, not a live
provider feed. At present it includes the exact `gpt-5-codex` API model only.
Claude events remain unknown until the adapter extracts and persists the
billing context needed to distinguish standard usage from service tiers and
other pricing modifiers. Normal startup, import, and reporting never fetch
pricing pages.

For each catalog change, maintainers should:

1. Verify every changed rate and billing modifier against the linked official
   provider documentation; record its URL and review date.
2. Add a new stable entry ID and per-entry pricing version for a new rate. Keep
   all immutable fields of released entries unchanged. An old entry may only be
   closed once by changing `effective_to` from null to its evidence-backed
   exclusive boundary; add the replacement with a new ID/version and matching
   `effective_from`.
3. Include only exact model identifiers and categories the adapters can
   establish. Do not map client aliases or assume subscription, cloud, region,
   service-tier, long-context, or cache-duration pricing.
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
