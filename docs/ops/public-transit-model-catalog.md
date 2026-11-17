# Explicit public model catalog

`public_transit_model_catalog` is an optional JSON setting for publishing a
verified model list when an existing public group has no channel-based catalog.
It changes only the public V1/V2 transit snapshots and the pages consuming them.
It does not change authenticated model discovery, request allowlists, routing,
customer permissions, group multipliers, or billing configuration.

The setting is absent/off by default. Example reviewed scope for Codebot:

```json
[
  {"group_id": 95, "models": ["claude-opus-5-5"]},
  {"group_id": 100, "models": ["deepseek-v4.1-flash", "glm-5.3"]}
]
```

Before enabling an entry, verify that the group's existing API can serve that
exact model. This setting is an operator declaration, not automatic capability
discovery. It does not turn successful traffic or monitor results into a model
catalog, and a failed probe never removes a published price.

Only active, non-exclusive groups with a concrete platform, no active channel
association, and an empty existing public model list are supplemented. Existing
allowlists are respected. Duplicate model names are ignored case-insensitively.
No group is created, and group IDs remain absent from public JSON.

Prices come from identified catalog entries, including recognized aliases,
with the group's configured flat token prices taking precedence. Missing input
or output prices, unsupported billing modes, and dynamic time pricing are
skipped. There is no family-name price guessing. A price of zero is preserved
only when explicitly configured in a group card; missing prices are not made
zero. Names and request platforms remain those of the verified model and group.

`claude-opus-5-5` and `glm-5.3` use identified catalog prices. The exact verified
model `deepseek-v4.1-flash` uses the application's existing billing constants
and peak/off-peak function, including the weekday 2x periods. Its public price
represents the snapshot time and discloses the schedule. Group custom prices
still take precedence without adding the peak multiplier, matching billing.
Other unrecognized DeepSeek names remain unpublished. No billing code or
customer prices are modified to populate this public catalog.

## Validation before deployment

Run from `backend`:

```sh
gofmt -w internal/service/public_transit_catalog.go internal/service/public_transit_catalog_test.go
go test -tags unit ./internal/service -run 'TestPublicTransit(ModelCatalog|SnapshotAuto)|TestPublicTransitGroups|TestBuildPublicTransitGroups' -count=1
go build ./cmd/server
```

Use a production-data copy with schedulers disabled for a snapshot dry run.
Capture snapshots with the setting absent and with the reviewed JSON above.
Compare the group lists by name and platform: only the approved groups' empty
model lists may gain the verified, priced entries. Multipliers, pre-existing
models/prices, billing and disclosure fields must remain equal. Monitor/cache
timestamps and observed statistics can legitimately change between requests.
Check actual pricing cards against billing records for each added model before
publishing, and inspect the final V1 and V2 JSON for credentials or internal IDs.

Back up the previous setting value before writing it. Removing the setting (or
restoring its previous value) immediately restores the prior public catalog on
subsequent snapshots without changing any customer account or group. Restoring
the previous application image is the independent code rollback. Neither
rollback should delete historical monitoring data.
