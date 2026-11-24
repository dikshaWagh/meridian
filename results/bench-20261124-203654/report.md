# Meridian routing benchmark

Generated 2026-11-24T21:16:03+05:30 · routing policy comparison

- Leader: `http://192.168.1.102:8080`
- Mode: `cluster`
- Models: `deepseek-r1-1.5b, llama-3.2-3b-instruct-q4_k_m, qwen2.5-0.5b-instruct-q4_k_m, qwen2.5-1.5b-instruct-q4_k_m, qwen2.5-3b-instruct-q4_k_m`
- Policies: `round-robin, least-connections, token-aware, random`
- Concurrency: `2,4`
- Requests per run: 40
- Workload profile: `mixed`
- Streaming: true

## What this run says

- Concurrency 2: lowest p95 end-to-end latency was token-aware (31887 ms, p50 12727 ms); highest goodput was random (0.14 req/s, 40 completion tok/s).
- Concurrency 4: lowest p95 end-to-end latency was random (42721 ms, p50 15570 ms); highest goodput was least-connections (0.21 req/s, 64 completion tok/s).
- At concurrency 4, token-aware beat least-connections on p95 by 9.6% (42833 vs 47368 ms).
- Overall: 319/320 requests succeeded (99.7%).

## Warnings

- 1 of 5 tested model(s) have a single healthy replica (deepseek-r1-1.5b). Requests to those models cannot be routed differently by any policy.
- The leader's routing signal tok_per_sec is flat (min 20.00, max 20.00). token-aware therefore falls back toward least-connections; see the per-replica comparison after the run.
- 1 request(s) failed. Check failure_by_status in summary.json before comparing latency percentiles.
- token-aware had a flat server-side speed signal (tok_per_sec 20.0..20.0) while client-observed generation rates differed (7.8..55.7 tok/s). In cluster mode the gateway does not feed measured tok/s back into the registry, so token-aware currently behaves like least-connections. This is a server limitation, not a benchmark error; the client-observed column in replicas.csv shows the real speed gap.
- least-connections at concurrency 4 was skewed (fairness CV 0.44, busiest replica 88%). Concurrent picks can herd before the reservation is taken; this is a real scheduling effect worth explaining.

## Results

| policy | conc | ok% | ttft p50 | ttft p95 | e2e p50 | e2e p95 | e2e p99 | req/s | tok/s | fairness CV | busiest |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| round-robin | 2 | 100.0 | 815 | 3893 | 12201 | 36219 | 48192 | 0.13 | 39 | 0.00 | 50% |
| round-robin | 4 | 100.0 | 1226 | 5704 | 17956 | 44633 | 52163 | 0.20 | 60 | 0.00 | 50% |
| least-connections | 2 | 100.0 | 216 | 1350 | 12032 | 33107 | 36796 | 0.13 | 44 | 0.25 | 75% |
| least-connections | 4 | 100.0 | 303 | 2668 | 13418 | 47368 | 52832 | 0.21 | 64 | 0.44 | 88% |
| token-aware | 2 | 100.0 | 206 | 1474 | 12727 | 31887 | 37448 | 0.14 | 42 | 1.00 | 100% |
| token-aware | 4 | 100.0 | 298 | 2479 | 14597 | 42833 | 46669 | 0.21 | 64 | 0.50 | 100% |
| random | 2 | 100.0 | 204 | 1758 | 11105 | 34160 | 43929 | 0.14 | 40 | 0.12 | 62% |
| random | 4 | 97.5 | 221 | 2617 | 15570 | 42721 | 60424 | 0.19 | 58 | 0.25 | 75% |

## Per-model p95 end-to-end latency (ms)

| run | deepseek-r1-1.5b | llama-3.2-3b-instruct-q4_k_m | qwen2.5-0.5b-instruct-q4_k_m | qwen2.5-1.5b-instruct-q4_k_m | qwen2.5-3b-instruct-q4_k_m |
|---|---:|---:|---:|---:|---:|
| round-robin @ 2 | 48774 | 26921 | 10181 | 33466 | 32374 |
| round-robin @ 4 | 38195 | 49124 | 13699 | 28058 | 46904 |
| least-connections @ 2 | 27354 | 23982 | 26920 | 28232 | 34763 |
| least-connections @ 4 | 50957 | 44744 | 13078 | 29034 | 48882 |
| token-aware @ 2 | 31492 | 25629 | 9413 | 36231 | 30437 |
| token-aware @ 4 | 41756 | 41776 | 13240 | 34405 | 46870 |
| random @ 2 | 42419 | 24520 | 5078 | 22425 | 34126 |
| random @ 4 | 60147 | 38969 | 17005 | 26725 | 45398 |

## Per-replica behaviour

| policy | conc | replica | node | model | reqs | share | observed tok/s | server tok/s start→end | served Δ |
|---|---:|---|---|---|---:|---:|---:|---|---:|
| round-robin | 2 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 25.8 | 20.0 → 20.0 | 8 |
| round-robin | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 4 | 10.0% | 13.7 | 20.0 → 20.0 | 4 |
| round-robin | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 4 | 10.0% | 14.2 | 20.0 → 20.0 | 4 |
| round-robin | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 4 | 10.0% | 59.8 | 20.0 → 20.0 | 4 |
| round-robin | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 4 | 10.0% | 54.6 | 20.0 → 20.0 | 4 |
| round-robin | 2 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 4 | 10.0% | 15.4 | 20.0 → 20.0 | 5 |
| round-robin | 2 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 4 | 10.0% | 19.7 | 20.0 → 20.0 | 4 |
| round-robin | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 4 | 10.0% | 16.4 | 20.0 → 20.0 | 4 |
| round-robin | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 4 | 10.0% | 14.8 | 20.0 → 20.0 | 4 |
| round-robin | 4 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 25.6 | 20.0 → 20.0 | 8 |
| round-robin | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 4 | 10.0% | 8.6 | 20.0 → 20.0 | 4 |
| round-robin | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 4 | 10.0% | 8.6 | 20.0 → 20.0 | 4 |
| round-robin | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 4 | 10.0% | 33.8 | 20.0 → 20.0 | 4 |
| round-robin | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 4 | 10.0% | 33.2 | 20.0 → 20.0 | 4 |
| round-robin | 4 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 4 | 10.0% | 15.4 | 20.0 → 20.0 | 4 |
| round-robin | 4 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 4 | 10.0% | 23.0 | 20.0 → 20.0 | 4 |
| round-robin | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 4 | 10.0% | 10.1 | 20.0 → 20.0 | 4 |
| round-robin | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 4 | 10.0% | 11.0 | 20.0 → 20.0 | 4 |
| least-connections | 2 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 28.0 | 20.0 → 20.0 | 8 |
| least-connections | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 2 | 5.0% | 15.6 | 20.0 → 20.0 | 2 |
| least-connections | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 6 | 15.0% | 13.5 | 20.0 → 20.0 | 6 |
| least-connections | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 5 | 12.5% | 54.4 | 20.0 → 20.0 | 5 |
| least-connections | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 3 | 7.5% | 51.9 | 20.0 → 20.0 | 3 |
| least-connections | 2 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 4 | 10.0% | 15.3 | 20.0 → 20.0 | 5 |
| least-connections | 2 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 4 | 10.0% | 25.8 | 20.0 → 20.0 | 4 |
| least-connections | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 3 | 7.5% | 16.0 | 20.0 → 20.0 | 3 |
| least-connections | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 5 | 12.5% | 14.9 | 20.0 → 20.0 | 5 |
| least-connections | 4 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 22.4 | 20.0 → 20.0 | 8 |
| least-connections | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 2 | 5.0% | 8.5 | 20.0 → 20.0 | 2 |
| least-connections | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 6 | 15.0% | 10.0 | 20.0 → 20.0 | 6 |
| least-connections | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 1 | 2.5% | 33.9 | 20.0 → 20.0 | 1 |
| least-connections | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 7 | 17.5% | 41.8 | 20.0 → 20.0 | 7 |
| least-connections | 4 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 3 | 7.5% | 15.4 | 20.0 → 20.0 | 3 |
| least-connections | 4 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 5 | 12.5% | 16.5 | 20.0 → 20.0 | 5 |
| least-connections | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 5 | 12.5% | 11.0 | 20.0 → 20.0 | 5 |
| least-connections | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 3 | 7.5% | 10.3 | 20.0 → 20.0 | 3 |
| token-aware | 2 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 29.1 | 20.0 → 20.0 | 8 |
| token-aware | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 8 | 20.0% | 14.4 | 20.0 → 20.0 | 8 |
| token-aware | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 0 | 0.0% | 0.0 | 20.0 → 20.0 | 0 |
| token-aware | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 8 | 20.0% | 55.7 | 20.0 → 20.0 | 8 |
| token-aware | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 0 | 0.0% | 0.0 | 20.0 → 20.0 | 0 |
| token-aware | 2 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 8 | 20.0% | 15.4 | 20.0 → 20.0 | 9 |
| token-aware | 2 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 0 | 0.0% | 0.0 | 20.0 → 20.0 | 0 |
| token-aware | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 8 | 20.0% | 15.9 | 20.0 → 20.0 | 8 |
| token-aware | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 0 | 0.0% | 0.0 | 20.0 → 20.0 | 0 |
| token-aware | 4 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 26.0 | 20.0 → 20.0 | 8 |
| token-aware | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 5 | 12.5% | 10.1 | 20.0 → 20.0 | 5 |
| token-aware | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 3 | 7.5% | 7.8 | 20.0 → 20.0 | 3 |
| token-aware | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 8 | 20.0% | 36.4 | 20.0 → 20.0 | 8 |
| token-aware | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 0 | 0.0% | 0.0 | 20.0 → 20.0 | 0 |
| token-aware | 4 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 6 | 15.0% | 15.3 | 20.0 → 20.0 | 6 |
| token-aware | 4 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 2 | 5.0% | 17.1 | 20.0 → 20.0 | 2 |
| token-aware | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 5 | 12.5% | 10.5 | 20.0 → 20.0 | 5 |
| token-aware | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 3 | 7.5% | 9.5 | 20.0 → 20.0 | 3 |
| random | 2 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 8 | 20.0% | 25.6 | 20.0 → 20.0 | 8 |
| random | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 4 | 10.0% | 12.8 | 20.0 → 20.0 | 4 |
| random | 2 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 4 | 10.0% | 15.7 | 20.0 → 20.0 | 4 |
| random | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 3 | 7.5% | 62.9 | 20.0 → 20.0 | 3 |
| random | 2 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 5 | 12.5% | 59.1 | 20.0 → 20.0 | 5 |
| random | 2 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 3 | 7.5% | 15.4 | 20.0 → 20.0 | 4 |
| random | 2 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 5 | 12.5% | 18.3 | 20.0 → 20.0 | 5 |
| random | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 4 | 10.0% | 15.5 | 20.0 → 20.0 | 4 |
| random | 2 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 4 | 10.0% | 14.0 | 20.0 → 20.0 | 4 |
| random | 4 | deepseek-r1-1.5b-b135f2-1 | b135f298806d3290c9ad802df709f0ac | deepseek-r1-1.5b | 7 | 17.9% | 21.0 | 20.0 → 20.0 | 7 |
| random | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 3 | 7.7% | 9.1 | 20.0 → 20.0 | 3 |
| random | 4 | llama-3.2-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | llama-3.2-3b-instruct-q4_k_m | 5 | 12.8% | 9.0 | 20.0 → 20.0 | 5 |
| random | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 4 | 10.3% | 38.7 | 20.0 → 20.0 | 4 |
| random | 4 | qwen2.5-0.5b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-0.5b-instruct-q4_k_m | 4 | 10.3% | 30.5 | 20.0 → 20.0 | 4 |
| random | 4 | qwen2.5-1.5b-instruct-q4_k_m-0c21f2-1 | 0c21f25f3ad0661131cb0b7d5a8d0558 | qwen2.5-1.5b-instruct-q4_k_m | 3 | 7.7% | 15.3 | 20.0 → 20.0 | 4 |
| random | 4 | qwen2.5-1.5b-instruct-q4_k_m-b135f2-1 | b135f298806d3290c9ad802df709f0ac | qwen2.5-1.5b-instruct-q4_k_m | 5 | 12.8% | 16.9 | 20.0 → 20.0 | 5 |
| random | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-1 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 2 | 5.1% | 10.0 | 20.0 → 20.0 | 2 |
| random | 4 | qwen2.5-3b-instruct-q4_k_m-3a833e-2 | 3a833eeb59a04bf9e9ecf1ccef86b7bb | qwen2.5-3b-instruct-q4_k_m | 6 | 15.4% | 11.0 | 20.0 → 20.0 | 6 |

## How to read the numbers

- **e2e**: wall-clock time from sending the request until the last response byte. This is what a user feels.
- **ttft**: time to the first content token on a streamed reply. Prefill and queueing dominate it. `n/a` when `--stream=false`.
- **p50/p95/p99**: percentiles over successful requests. p95 is the useful planning number; p99 shows tail pain.
- **req/s** and **tok/s**: successful requests and completion tokens divided by the measured wall-clock run duration (goodput, not raw load).
- **fairness CV**: coefficient of variation of request counts across healthy replicas of the same model. 0 = perfectly even; higher = more skew. token-aware may skew on purpose toward faster replicas.
- **busiest**: share of requests handled by the single busiest replica. For N equally fast replicas, an even split is 100/N%.
- **observed tok/s**: client-side completion tokens divided by generation time (e2e − ttft for streams). Compare it with the server's `tok_per_sec` to see whether the router had a real speed signal.

## Files

- `report.html` — charts and tables (open this)
- `summary.json` — machine-readable run summaries
- `summary.csv` — one row per policy × concurrency
- `requests.csv` — every request with replica, latency and tokens
- `timeseries.csv` — leader `/v1/status` snapshots (in-flight, tok/s, CPU, RAM)
- `replicas.csv` — per-replica distribution and speed signal
