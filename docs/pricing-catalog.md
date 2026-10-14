# Pricing catalog maintenance

The library ships a small offline public API list-price catalog at
`plugins/library/token-logger/pricing-catalog.json`. It is data, not a live
provider feed. Normal startup, import, and reporting never fetch pricing pages.

For each catalog change, maintainers should:

1. Verify every changed rate and billing modifier against the linked official
   provider documentation; record its URL and review date.
2. Add a new stable entry ID/version for a new supported rate. Do not rewrite
   rates already released: set the old entry's exclusive `effective_to` and
   add the replacement with its evidence-backed `effective_from`.
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
inserts a missing bundled `id`; it does not update account- or user-specific
rules. A higher-precedence exact user rule may intentionally customize a
bundled rate.

JSON and CSV reports expose the matched model selector and effective interval
along with catalog version and source, so grouped results that combine multiple
entries visibly report `mixed` provenance instead of implying a single rule.

Bundled values describe public API list-price equivalents, not invoices. They
exclude taxes, credits, negotiated pricing, subscription entitlements, and
provider resellers. If the source data lacks a required billing modifier, keep
the result unknown and explain the missing context instead of guessing.
