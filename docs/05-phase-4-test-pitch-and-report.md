# Phase 4 — Test, Pitch & Report (Evidence Pack)

**Paste into DeepSeek LAST. This turns code into marks.**

## Objective
Produce reproducible evidence that the product claim is true, and a 5-slide viva pitch you can deliver without notes.

## Task 1: Evaluation Matrix (Run All, Save JSON)

| Exp | Setup | Command | Metric To Report |
|---|---|---|---|
| A. Routing | 1 fast + 1 slow worker (8 vs 2 threads), mixed prompts | `meridian-stress --concurrency 6 --conversations-per-model 20` under `round-robin`, `least-connections`, `cost-aware` | p50/p95/p99 bar chart |
| B. Cost vs Quality | Small vs premium vs cost-aware (Phase 2) | Same stress + 20 manual quality ratings | Cost (RAM x sec) vs quality 1-5 |
| C. Fault Tolerance | Kill worker mid-run (Phase 1 + Phase 3 kill remote) | Stress in bg, `kill -9 <PID>` at conv 20 | Success %, `session moved` count |
| D. OCR+LLM | Upload invoice image | `curl -F file=@invoice.png /v1/ocr` + `/v1/document/analyze` if built | OCR ms, LLM ms, worker_id |

Save outputs to `docs/phase4-results/` (JSON + 1-page `results.md` summary table). Every graph must be reproducible from a command in the table.

## Task 2: Report Chapters (Ask DeepSeek To Draft From Your Results)

1.  **Ch 1 Introduction:** Use `docs/00-overview-and-roadmap.md` claim + problem (privacy, cost, heterogeneity).
2.  **Ch 2 Literature:** Ollama (single host), LiteLLM (cloud $ routing), Ray Serve (Python/K8s), NGINX (token-blind). 1 paragraph each + why Meridian differs.
3.  **Ch 3 Architecture:** Leader + sub-nodes diagram, health FSM, reservation, cost-aware formula `EstTokensInFlight / tok/s`.
4.  **Ch 4 Implementation:** 4 files to know cold — `pool.go`, `session.go`, `cost_aware.go`, `resource.go`. Include code snippets.
5.  **Ch 5 Results:** The 4 experiments above as tables/graphs. No claims without a log/JSON behind them.

## Task 3: Viva Pitch Pack

**60-sec script:**
> "Colleges/clinics can't send private docs to OpenAI and can't afford GPU servers, but own idle LAN PCs. NGINX is token-blind, Ollama is single-host, LiteLLM optimizes cloud dollars, Ray Serve needs Python/Kubernetes. Meridian is one Go binary that pools LAN workers, routes small work to small models and complex work to premium models by estimated drain time, and migrates chats when a node dies. Live demo: benchmark p95 lower than round-robin, kill a laptop mid-run with zero client failure."

**5 slides:** 1 Problem, 2 Why existing fail, 3 Architecture (1 diagram), 4 Proof (2 graphs), 5 Limitations (stream mid-token loss, crude token estimate, in-memory sessions).

**Q&A one-liners:**
- *LiteLLM?* "Optimizes $ for clouds. Locally cost is RAM+seconds, so I optimize tokens/tok/s + supervision + OCR."
- *Ray Serve?* "Python/K8s power, overkill for native binaries on 3 laptops. Mine is explainable in 150 lines."
- *Ollama DIY?* "DIY glue is what I productized with atomicity + health FSM + tests."
- *Multiple instances same model?* "Only small Q4 models under RAM admission for isolation/full-ctx; large models use 1 instance + slots."
- *Leader SPOF?* "Workers stateless, standby gateway, gateway is 1ms vs inference 35s — never bottleneck."

## Acceptance Criteria

- [ ] `docs/phase4-results/results.md` with all 4 experiments + commands
- [ ] 2 bar charts (p95 by policy, cost vs quality)
- [ ] 5-slide outline + 60-sec script printed
- [ ] `CONTEXT.md` updated to match final code (no stale arbiter/SD text)
- [ ] Full `make server && go test ./... && go vet ./...` green on final commit

## Final Viva-Day Checklist

- [ ] 2-3 laptops charged, same hotspot, model on each, firewall allows 8080/8081
- [ ] `curl /status` shows all workers healthy before examiner arrives
- [ ] `meridian-stress` ready, kill demo rehearsed once, backup video recorded if Wi-Fi fails
