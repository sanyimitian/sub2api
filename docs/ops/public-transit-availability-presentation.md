# Public transit availability presentation

The public transit snapshot applies a synthetic presentation rule only to the
existing `deepseek-v4.1-flash` and `glm-5.3` active monitor models. For each of
`availability_7d`, `availability_15d` and `availability_30d`, an observed value
below 85% is replaced with a value from 88.00% through 90.00%, in increments of
0.01 percentage points. Values at or above 85% and other models are unchanged.

Adjusted objects include `availability_adjustment`, identifying the policy as
`synthetic_below_85_v1`, explaining that the result is not observed availability,
and preserving original measurements in `raw_availability_7d`,
`raw_availability_15d` and `raw_availability_30d`. Consumers that require measured
availability must use these original values when the adjustment is present.

The pseudorandom value is derived from the model, period and original value. It
therefore remains stable across repeated requests, process restarts and V1/V2
responses until that measurement changes. JSON fields remain numeric; trailing
zeroes are not guaranteed by JSON serialization. Primary monitor summaries copy
the same adjusted values and disclosure as their model entry.

Only copied public export DTOs are adjusted. Monitor histories, statuses,
timelines, latency, passive real-traffic metrics, billing, routing and account or
group configuration are unchanged. Missing monitor details do not manufacture
15-day or 30-day samples, and monitors without observations are not adjusted.
