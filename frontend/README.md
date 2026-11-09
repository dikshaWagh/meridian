# Meridian Frontend

A live console for the Meridian LLM balancer, plus an isolated, mock-only ML
routing preview. Built with React, TypeScript, Vite, Tailwind CSS, Framer
Motion, Recharts and React Router.

- **LLM pages use real backend data** through the Vite dev proxy.
- **A local simulation fallback** mirrors the balancer's health logic and is
  clearly labelled **Simulated**.
- **The ML page is a deterministic mock** and makes **zero network calls**; it is
  labelled **Mock** everywhere.

## Requirements

- Node.js 20+ (Vite 6) and npm
- A running Meridian backend for live mode (optional — Simulation works alone)

## Quick start

```bash
cd frontend
npm install
npm run dev
```

Open the URL printed by Vite (default <http://localhost:5173>).

### Running Meridian for live mode

From the repository root, in mock-worker mode:

```bash
go run ./cmd/meridian-server serve --mock --host 127.0.0.1 --port 8080
```

Then keep the frontend's default proxy target (`http://localhost:8080`) or point
`VITE_MERIDIAN_URL` at the server. The UI polls `GET /status` and
`GET /v1/models` every ~2 seconds and pauses while the tab is hidden.

## Modes

The mode switch in the header toggles between **Live** and **Simulated**. The
choice persists in `localStorage` under `meridian.mode`.

| Mode | Dashboard / Playground | Data source |
|---|---|---|
| **Live** | Real workers, models and chat completions | `GET /status`, `GET /v1/models`, `POST /v1/chat/completions` |
| **Simulated** | Local mirror of the same shapes; Kill / Fail probes / Revive / Reset controls | In-browser engine only |
| **ML Preview** | Always mock, regardless of mode | In-browser mock engine only |

Simulated workers are badged **Simulated**; the ML page carries a permanent
**Mock — not connected to Meridian** banner. No simulated or mock value is ever
presented as live telemetry.

## Environment variables

Copy `.env.example` to `.env` if you need to change the defaults.

| Variable | Default | Purpose |
|---|---|---|
| `VITE_MODE` | `live` | Initial mode when no saved preference exists (`live` or `simulation`). |
| `VITE_MERIDIAN_URL` | `http://localhost:8080` | Upstream used by the **Vite dev proxy** (config only, not in the browser bundle). |
| `VITE_API_BASE` | empty | Optional prefix for `fetch` calls. Leave empty for same-origin requests. |

### Why the proxy?

Meridian's Go server sends no CORS headers, so the browser cannot call it
directly from another origin. `vite.config.ts` proxies `/status`, `/v1/*` and
`/healthz` to `VITE_MERIDIAN_URL`, keeping frontend requests same-origin. This
is why no Go handler or CORS change was needed.

For a production build, serve `dist/` behind a reverse proxy that forwards
`/status` and `/v1/*` to Meridian, or set `VITE_API_BASE` to a same-origin
prefix that does.

## Scripts

| Command | Description |
|---|---|
| `npm run dev` | Vite dev server with the Meridian proxy. |
| `npm run build` | Type-check, then build to `dist/`. |
| `npm run preview` | Serve the production build locally. |
| `npm run lint` | ESLint. |
| `npm run typecheck` | `tsc --noEmit`. |

## Visual system

The UI is styled as a light technical drawing: `#F2F2F2` ground, charcoal
1px linework, chamfered panels, and a single orange accent (`#E8581C`, with
`#C2410C` for orange text). Panels use flat `#F8F8F8` / `#EBEBEB` fills with no
radius, shadow, blur, glow or gradients. ML surfaces use dashed slate borders
and a **Mock** tag so they cannot be mistaken for live data.

- Fonts are self-hosted `@fontsource` **Geist Sans** and **Geist Mono**; no
  external font requests.
- Reusable decorative primitives live in `components/ui/hud/`
  (`ChamferFrame`, `CornerBrackets`, `DashedCorners`, `TickStrip`,
  `MicroTextBlock`, `PixelGlyph`). They are `aria-hidden`, `pointer-events:
  none`, absolutely positioned and do not affect layout.
- Charts use hairline strokes/grid lines, ink or orange series and no gradient
  fills. Status is always shown as marker + text, never colour alone.
- Text contrast was audited against the actual composited backgrounds and meets
  WCAG AA; focus is a visible orange ring and `prefers-reduced-motion` is
  honoured.

## Structure

```text
frontend/
├── src/
│   ├── components/
│   │   ├── layout/       App shell, navigation, mode switch, page header
│   │   ├── ui/           Button, Badge, Panel, Stat, StatusDot, states, icons
│   │   ├── ui/hud/       Decorative corner frames, ticks, micro-text, glyphs
│   │   └── viz/          Round-robin diagram, observed chart, ML score chart
│   ├── hooks/
│   │   ├── useMeridianStatus.ts  Live polling (visible tab only) + offline state
│   │   ├── useSimulation.ts      Local LLM balancer simulation
│   │   ├── useMockML.ts          Mock ML routing state
│   │   └── useObservedHistory.ts Rolling chart history
│   ├── lib/              API client, types, mode context, constants, formatters
│   ├── mock/ml/          Deterministic ML engine and scoring weights (no network)
│   ├── pages/            Landing, Dashboard, Playground, MLPreview, NotFound
│   ├── simulation/       LLM simulation engine + token estimator
│   └── styles/           Design tokens and global styles
├── .env.example
├── vite.config.ts        Dev proxy configuration
└── go.mod                Nested module boundary (see NOTES.md)
```

Components do not fetch. Data enters through hooks only, and the live and
simulated hooks expose the same worker-status shape.

## State and accessibility

- Loading, error, empty, success and disabled states are implemented for every
  data view.
- Explicit offline handling: the Dashboard shows a **Backend offline** banner and
  last-known snapshot; the Playground reports **Backend unreachable**.
- HTTP errors are mapped to actionable UI: 400, 404, 502, 503.
- Keyboard focus is visible; tables and charts have text alternatives; status is
  never conveyed by colour alone.
- `prefers-reduced-motion` disables non-essential animation.
- Layouts were checked at 360 / 768 / 1280 / 1920 px with no horizontal page
  overflow; wide tables scroll inside their panel. Panel positions and widths
  match the pre-restyle captures at every breakpoint.

## ML mock

`src/mock/ml/` implements model → health filter → weighted score. Weights are
`latency 0.40`, `queue 0.25`, `gpu 0.20`, `cpu 0.15`; the lowest score wins.
CPU-only workers have no GPU metric and score 0 for that component. The engine
is deterministic per interaction and never calls the network.

## Notes

See [NOTES.md](./NOTES.md) for verified behaviour, assumptions, and anything
that could not be tested in this environment.
