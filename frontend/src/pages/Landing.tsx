import { Badge } from '../components/ui/Badge'
import { CodeBlock } from '../components/ui/CodeBlock'
import { Icon } from '../components/ui/Icon'
import { LinkButton } from '../components/ui/LinkButton'
import { Panel } from '../components/ui/Panel'
import { ChamferFrame, DashedCorners } from '../components/ui/hud'
import { RoundRobinDiagram } from '../components/viz/RoundRobinDiagram'

const quickstartTerminal = `go build -o bin/meridian-server ./cmd/meridian-server
./bin/meridian-server serve --mock`

const quickstartCurl = `curl http://localhost:8080/v1/chat/completions \\
  -H 'Content-Type: application/json' \\
  -d '{"model":"qwen2.5-3b","messages":[{"role":"user","content":"Hello"}]}'`

const quickstartFrontend = `cd frontend
npm install
npm run dev`

const endpoints = [
  { method: 'POST', path: '/v1/chat/completions', note: 'OpenAI-style request, max 1 MiB' },
  { method: 'GET', path: '/v1/models', note: 'Only models with a healthy worker' },
  { method: 'GET', path: '/status', note: 'Per-worker state and counters' },
  { method: 'GET', path: '/healthz', note: 'Balancer liveness' },
]

const healthSteps = [
  {
    step: '01',
    title: 'Probe every 2 s',
    body: 'The balancer calls GET /health on each worker. 200 is healthy, 503 is not ready, anything else is a failure.',
    badge: '2 s interval',
    tone: 'accent' as const,
  },
  {
    step: '02',
    title: 'Three strikes',
    body: 'Three consecutive failures move a healthy worker to unhealthy. A starting worker gets a 180 s readiness grace period.',
    badge: '3 failures',
    tone: 'warn' as const,
  },
  {
    step: '03',
    title: 'Recover on success',
    body: 'The next successful probe restores health. A crashed process is not restarted; it stays unhealthy until the process is back.',
    badge: 'next success',
    tone: 'ok' as const,
  },
]

export default function Landing() {
  return (
    <div className="space-y-14 pb-2 sm:space-y-16">
      <section className="grid items-start gap-8 lg:grid-cols-[1.05fr_0.95fr] lg:gap-10">
        <div className="pt-1">
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="accent" dot>
              LLM balancer
            </Badge>
            <Badge tone="ml">ML inference · in progress</Badge>
          </div>

          <h1 className="mt-5 text-4xl font-semibold leading-[1.08] tracking-tight text-ink sm:text-5xl">
            One endpoint.
            <br />
            <span className="relative inline-block text-ink">
              Many workers.
              <span aria-hidden="true" className="absolute -bottom-2 left-0 h-px w-16 bg-accent" />
            </span>
          </h1>

          <p className="mt-5 max-w-xl text-[15px] leading-relaxed text-dim">
            Meridian is a Go LLM balancer. It supervises llama-server workers, checks their health,
            and routes OpenAI-compatible requests with per-model round robin across the healthy
            ones. This console makes that behaviour visible.
          </p>

          <div className="mt-7 flex flex-wrap items-center gap-3">
            <LinkButton to="/dashboard" arrow>
              Open dashboard
            </LinkButton>
            <LinkButton to="/playground" variant="secondary">
              Try a request
            </LinkButton>
          </div>

          <div className="mt-8 grid grid-cols-2 gap-3 sm:grid-cols-4">
            {[
              { value: '2 s', label: 'health probe' },
              { value: '3', label: 'failures → unhealthy' },
              { value: '180 s', label: 'starting grace' },
              { value: '1 MiB', label: 'max request body' },
            ].map((item) => (
              <div key={item.label} className="rounded-none border border-line bg-panel px-3 py-2.5">
                <div className="font-mono text-lg font-semibold text-ink">{item.value}</div>
                <div className="mt-0.5 text-[10px] uppercase tracking-wider text-dim">{item.label}</div>
              </div>
            ))}
          </div>
        </div>

        <Panel
          title="Routing model"
          subtitle="Model first, then health, then round robin. No load or score inputs."
          actions={<Badge tone="accent">Live logic</Badge>}
          className="corner-ticks"
        >
          <RoundRobinDiagram />
        </Panel>
      </section>

      <section>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <p className="eyebrow">Health · failure · recovery</p>
            <h2 className="mt-2 text-2xl font-semibold tracking-tight text-ink">
              Availability is a state machine, not a guess.
            </h2>
          </div>
          <p className="max-w-md text-xs leading-relaxed text-dim">
            A busy worker is still healthy. Only consecutive probe failures remove it from
            round-robin candidates.
          </p>
        </div>

        <div className="mt-5 grid gap-3 md:grid-cols-3">
          {healthSteps.map((item) => (
            <div key={item.step} className="panel corner-ticks p-5">
              <ChamferFrame />
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-[11px] text-dim">{item.step}</span>
                <Badge tone={item.tone}>{item.badge}</Badge>
              </div>
              <h3 className="mt-4 text-sm font-semibold text-ink">{item.title}</h3>
              <p className="mt-2 text-xs leading-relaxed text-dim">{item.body}</p>
            </div>
          ))}
        </div>

        <div className="mt-3 rounded-none border border-line bg-panel px-4 py-3">
          <p className="text-[11px] leading-relaxed text-dim">
            Token estimates travel in response headers:{' '}
            <span className="font-mono text-[10px] text-ink">X-Meridian-Est-Input-Tokens</span>,{' '}
            <span className="font-mono text-[10px] text-ink">X-Meridian-Est-Output-Tokens</span> and{' '}
            <span className="font-mono text-[10px] text-ink">X-Meridian-Est-Total-Tokens</span>,
            alongside <span className="font-mono text-[10px] text-ink">X-Meridian-Worker</span> and{' '}
            <span className="font-mono text-[10px] text-ink">X-Meridian-Model</span>. Actual usage is
            logged server-side.
          </p>
        </div>
      </section>

      <section className="grid gap-5 lg:grid-cols-[0.92fr_1.08fr]">
        <div className="min-w-0">
          <p className="eyebrow">Quickstart</p>
          <h2 className="mt-2 text-2xl font-semibold tracking-tight text-ink">
            Build it, mock it, call it.
          </h2>
          <p className="mt-3 max-w-lg text-sm leading-relaxed text-dim">
            Mock mode spawns fake llama-server workers, so the dashboard and playground are usable
            without model files or a GPU. Point the frontend proxy at the balancer with{' '}
            <span className="font-mono text-[12px] text-ink">VITE_MERIDIAN_URL</span>.
          </p>

          <ul className="mt-5 space-y-2">
            {endpoints.map((endpoint) => (
              <li
                key={endpoint.path}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-1 rounded-none border border-line bg-panel px-3 py-2"
              >
                <span className="w-10 font-mono text-[10px] uppercase tracking-wider text-accent-ink">
                  {endpoint.method}
                </span>
                <span className="font-mono text-xs text-ink">{endpoint.path}</span>
                <span className="text-[11px] text-dim">· {endpoint.note}</span>
              </li>
            ))}
          </ul>

          <p className="mt-4 flex items-start gap-2 text-[11px] leading-relaxed text-dim">
            <Icon name="alert" className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" />
            The Go server sends no CORS headers. The dev server proxies{' '}
            <span className="font-mono text-[10px] text-ink">/status</span>,{' '}
            <span className="font-mono text-[10px] text-ink">/v1/*</span> and{' '}
            <span className="font-mono text-[10px] text-ink">/healthz</span> to Meridian.
          </p>
        </div>

        <div className="min-w-0 space-y-3">
          <CodeBlock code={quickstartTerminal} label="terminal · mock worker mode" />
          <CodeBlock code={quickstartCurl} label="curl · chat completion" />
          <CodeBlock code={quickstartFrontend} label="terminal · frontend dev server" />
        </div>
      </section>

      <section className="panel corner-ticks relative overflow-hidden border-dashed">
        <ChamferFrame tone="accent" />
        <DashedCorners />
        <div className="grid gap-6 p-6 sm:p-8 lg:grid-cols-[1.15fr_0.85fr] lg:items-center">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone="ml" dot>
                Mock preview
              </Badge>
              <Badge tone="warn">In progress</Badge>
            </div>
            <h2 className="mt-4 text-2xl font-semibold tracking-tight text-ink">
              A second, separate routing problem.
            </h2>
            <p className="mt-3 max-w-xl text-sm leading-relaxed text-dim">
              ML inference needs model-aware scoring instead of round robin. The preview simulates
              two-stage routing: model, then health, then a weighted score over latency, queue, GPU
              and CPU. Lowest score wins.
            </p>
            <div className="mt-5 flex flex-wrap gap-2">
              {['0.40 latency', '0.25 queue', '0.20 gpu', '0.15 cpu'].map((weight) => (
                <span
                  key={weight}
                  className="rounded-none border border-ml bg-mock-soft px-2 py-1 font-mono text-[10px] text-ml"
                >
                  {weight}
                </span>
              ))}
            </div>
            <div className="mt-5">
              <LinkButton to="/ml" variant="secondary" arrow>
                Explore the mock
              </LinkButton>
            </div>
          </div>

          <div className="relative rounded-none border border-ml bg-panel-alt p-4">
            <div className="flex items-center justify-between gap-2">
              <span className="mono-label">resnet50 candidates</span>
              <span className="font-mono text-[10px] text-ml">mock</span>
            </div>
            <div className="mt-3 space-y-2">
              {[
                { id: 'resnet-1', score: '0.284', state: 'selected' },
                { id: 'resnet-2', score: '0.512', state: 'candidate' },
                { id: 'resnet-3', score: '0.661', state: 'cpu only' },
              ].map((row) => (
                <div
                  key={row.id}
                  className={`flex items-center justify-between rounded-none border px-3 py-2 ${
                    row.state === 'selected'
                      ? 'border-accent bg-accent-soft'
                      : 'border-line bg-panel'
                  }`}
                >
                  <span className="font-mono text-[11px] text-ink">{row.id}</span>
                  <span className="font-mono text-[10px] text-dim">score {row.score}</span>
                </div>
              ))}
            </div>
            <p className="mt-3 text-[10px] leading-relaxed text-dim">
              Illustrative mock values. Open the preview to generate your own decisions.
            </p>
          </div>
        </div>
      </section>

      <section className="flex flex-col items-start justify-between gap-4 rounded-none border border-line bg-panel px-5 py-4 sm:flex-row sm:items-center">
        <div>
          <p className="text-sm font-medium text-ink">See the real thing</p>
          <p className="mt-1 text-xs text-dim">
            Dashboard for live state, Playground for a request against a running balancer.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <LinkButton to="/dashboard" arrow>
            Dashboard
          </LinkButton>
          <LinkButton to="/playground" variant="secondary">
            Playground
          </LinkButton>
        </div>
      </section>
    </div>
  )
}
