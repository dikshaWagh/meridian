# Frontend Notes

Open questions, assumptions, and anything that could not be verified in this
environment. Anything unverified here is **not** claimed as working.

## Verified in this environment

- **Live LLM data** against `meridian-server serve --mock` through the Vite
  proxy:
  - `GET /status` polling (≈2 s) and `GET /v1/models`.
  - Per-model round robin: consecutive requests to the same model alternated
    between `w1` and `w2`.
  - `POST /v1/chat/completions` returned a real completion with
    `X-Meridian-Worker`, `X-Meridian-Model` and `X-Meridian-Est-*-Tokens`
    headers; those values render in the Playground.
  - Unknown model → 404 JSON shown as **Unknown model (404)**.
  - Killing all `qwen2.5-3b` workers → 503
    `no healthy worker for model "qwen2.5-3b"` shown as **No healthy worker (503)**.
  - 400 and 502 handling is implemented from the documented request/upstream
    contract, but those two status codes were **not induced against the live
    backend**. Their UI paths were reviewed in code, not observed end-to-end.
- **Simulation mirror**: three failing probes on `w1` produced
  `3 / 3` and **Unhealthy** with `consecutive health check failures`; **Revive**
  returned it to starting → **Healthy**. **Kill** on `w2` marked
  `process exited` and **Unhealthy**. **Reset** restores the initial snapshot.
- **Offline states**: with the backend stopped, Dashboard shows the **Backend
  offline** banner, the last-known/empty state, and a Retry action; Playground
  shows the unreachable notice and a send attempt resolves to **Backend
  unreachable** instead of a misleading HTTP error. The Vite proxy's non-JSON
  500 response is classified as a network/offline condition.
- **ML mock**: deterministic routing decisions, candidate table, score
  breakdown chart, and Load / Kill / Revive interactions. The page makes no
  network requests.
- **Responsive checks** at 360 / 768 / 1280 / 1920 px: 0 px horizontal page
  overflow; wide tables scroll inside their panels.
- **Build checks**: `npm run lint`, `npm run typecheck` and `npm run build`
  pass. `go build ./...` and `go test ./...` pass (see race limitation below).
- No unexpected console errors in the normal success flow. Expected browser
  `Failed to load resource` messages appear during deliberate 404/503/offline
  tests because the requests genuinely fail.

## Restyle verification (technical-drawing pass)

- **Before/after captures** exist for Landing, Dashboard, Playground and ML at
  360 / 768 / 1280 / 1920 px. Panel counts match, every panel's `x` and width
  match the pre-restyle capture exactly (0.0 px difference), and the page has
  0 px horizontal overflow at every width. Heights differ by small amounts
  because Geist's metrics and text wrapping differ from the previous system
  font (largest shift ≈ 79 px on the 1280 px dashboard); grid/flex structure,
  panel positions and breakpoints are unchanged.
- **Contrast audit** walks every rendered text node, composites the actual
  background (including translucent fills) and reports 0 failures below WCAG AA
  on Landing, Dashboard, Playground, ML Preview and 404. The orange focus ring
  is a visible 2 px outline; `prefers-reduced-motion` keeps the round-robin
  diagram static.
- **Panel/chrome checks**: computed radius `0px`, box-shadow `none`,
  backdrop-filter `none`, 10 px `clip-path` chamfer, Geist Sans/Mono loaded from
  the bundle, no external font requests.
- **Interaction flows re-run after the restyle**: live dashboard + chart, live
  Playground completion, simulation Kill → Revive and Fail probes → Unhealthy,
  simulated Playground response, Mock routing decision, and offline banners
  (via aborted requests in live mode) all pass.
- **Badge tones**: `Badge` composes `badge-${tone}` at runtime, so the six tone
  classes are in the Tailwind `safelist`; they are present in the production
  CSS as well as in dev.
- **Verification scripts** (screenshots, box comparison, contrast, a11y and
  flow checks) were kept outside the repository.

### Restyle deviations

- `--ink-dim` is `#666666` rather than `#6B6B6B`. The specified value measures
  ~4.47:1 on the `#EBEBEB` tonal plane, just under AA for small text; `#666`
  clears it at ~4.8:1.
- `--warn` is a deep amber `#7A5200` instead of a mid amber. The lighter amber
  failed AA for 10 px badge text on tinted surfaces; green `#1F6B45` and crimson
  `#B42318` are unchanged.
- The score-composition chart keeps stacked **fills** because the four
  weighted components must be distinguishable; it uses only the ink/orange/
  slate/grey family and no gradients. The observed line chart uses a 1 px
  stroke and no fill.
- The requested "subtle flat tonal planes" are expressed through the panel
  fills (`#F8F8F8` / `#EBEBEB`); an earlier page-level tonal rectangle cut
  across sections at arbitrary viewport positions, so it was removed in favour
  of the drafting grid.
- At 360 px the round-robin endpoint strip wraps to a second line so every
  label stays visible instead of being clipped or hidden.

## Untested or out of scope

- **`go test -race ./...` was not run.** This Windows environment has no C
  compiler, so cgo/race builds fail with `gcc not found`. The non-race
  `go test ./...` passes. No Go files were modified.
- **Streaming is not rendered.** Playground sends non-streaming requests only,
  as required. If Meridian's `stream: true` SSE path is used elsewhere, the
  frontend does not parse it.
- **The ML preview is not validated against `internal/ml/`.** Per instructions,
  it follows the mock specification only and is not wired to real ML endpoints
  or metrics. Its scores are illustrative, deterministic, and badged **Mock**.
- **No load/stress testing.** The simulation matches the observed 2 s probe
  interval, 3-failure threshold, and 180 s starting grace period, but timing
  under real load or long-running sessions was not stress-tested.
- **Production hosting was not exercised.** Only the Vite dev proxy path was
  tested. `VITE_API_BASE` exists for a same-origin reverse proxy but no such
  deployment was available to verify. Direct cross-origin calls will fail
  because Meridian sends no CORS headers.
- **`/healthz` is proxied but not polled by the UI.** Worker health is derived
  from `GET /status`; there is no separate frontend health probe.
- **No authentication, persistence, or multi-user behaviour** — not part of the
  Meridian API surface used here.
- **Exact worker snapshot fields** were mapped from the running mock backend
  (`id`, `model`, `port`, `pid`, `state`, `healthy`, `in_flight`, `served`,
  `est_tokens_in_flight`, `consecutive_failures`, `last_check`, `last_error`).
  Additional fields outside this shape are ignored, not invented.

## Assumptions

- The balancer continues to expose `GET /status`, `GET /v1/models`,
  `POST /v1/chat/completions`, and the `X-Meridian-*` response headers. The
  metadata panel is only populated when those headers are present.
- Live token estimates come from the backend headers, not from the frontend
  estimator. The local estimator is used only for simulated usage.
- If the backend is unreachable, the Vite proxy may return a non-JSON 5xx page.
  That condition is deliberately surfaced as **offline / unreachable** rather
  than as a generic HTTP error.
- `localStorage` persistence is best-effort; private browsing that rejects
  storage still works in memory for the session.

## Structural notes

- **Nested `frontend/go.mod`.** `npm install` places third-party packages
  (notably a Go-looking directory inside `node_modules`) that the root Go
  module would otherwise discover. The nested `go.mod` makes `frontend/` a
  separate Go module boundary so `go build ./...` / `go test ./...` continue to
  cover only Meridian's Go code. It contains no Go source and does not change
  the root module.
- **Mode persistence** uses `meridian.mode`. `VITE_MODE` only supplies the
  initial value; a user's switch wins on later visits.
- **Owner outside `frontend/`:** the only change is the three appended lines in
  the root `.gitignore` (`node_modules/`, `dist/`, `.env`). No `cmd/`,
  `internal/`, `go.mod`, `go.sum`, `bin/`, or `meridian.ml.example.yaml`
  changes are intended or left behind.
