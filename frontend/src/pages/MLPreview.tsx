import { useState } from 'react'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Icon } from '../components/ui/Icon'
import { Panel } from '../components/ui/Panel'
import { StatusDot } from '../components/ui/StatusDot'
import { EmptyState } from '../components/ui/States'
import { PageHeader } from '../components/layout/PageHeader'
import { CornerBrackets, DashedCorners, MicroTextBlock } from '../components/ui/hud'
import { ScoreBreakdown } from '../components/viz/ScoreBreakdown'
import type { ScoreDatum } from '../components/viz/ScoreBreakdown'
import { useMockML } from '../hooks/useMockML'
import { ML_WEIGHTS } from '../mock/ml/weights'
import type { MLCandidate, MLDecision, MLWorker } from '../mock/ml/engine'
import { formatNumber } from '../lib/formatters'

const weights = [
  { key: 'latency', label: 'Latency', value: ML_WEIGHTS.latency, tone: 'var(--cyan)' },
  { key: 'queue', label: 'Queue', value: ML_WEIGHTS.queue, tone: 'var(--blue)' },
  { key: 'gpu', label: 'GPU', value: ML_WEIGHTS.gpu, tone: 'var(--purple)' },
  { key: 'cpu', label: 'CPU', value: ML_WEIGHTS.cpu, tone: 'var(--teal)' },
] as const

function statusTone(status: MLDecision['status']) {
  if (status === 200) return 'ok' as const
  if (status === 503) return 'warn' as const
  return 'bad' as const
}

function scoreString(candidate: MLCandidate): string {
  return candidate.score === null ? '—' : candidate.score.toFixed(3)
}

function normString(value: number | null | undefined): string {
  return value === null || value === undefined ? '—' : value.toFixed(3)
}

function decisionTime(at: number): string {
  return new Date(at).toLocaleTimeString([], { hour12: false })
}

function WorkerCard({
  worker,
  decision,
  onLoad,
  onKill,
  onRevive,
}: {
  worker: MLWorker
  decision: MLDecision | null
  onLoad: () => void
  onKill: () => void
  onRevive: () => void
}) {
  const candidate = decision?.candidates.find((item) => item.workerId === worker.id)
  const selected = decision?.chosen === worker.id
  const unavailable = worker.state === 'unhealthy'

  return (
    <div
      className={`relative rounded-none border p-4 transition-colors ${
        selected ? 'border-accent bg-accent-soft' : 'border-line bg-panel'
      }`}
    >
      {selected ? <CornerBrackets /> : null}
      <div className="flex items-start justify-between gap-2">
        <div>
          <div className="flex items-center gap-2">
            <Icon name="cpu" className="h-3.5 w-3.5 text-ml" />
            <span className="font-mono text-xs text-ink">{worker.id}</span>
          </div>
          <p className="mt-1 font-mono text-[10px] text-dim">
            {worker.model}
            {worker.cpuOnly ? ' · CPU only' : ''}
          </p>
        </div>
        <div className="flex flex-col items-end gap-1">
          <StatusDot state={worker.state} />
          {selected ? <Badge tone="ml">selected</Badge> : null}
        </div>
      </div>

      <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-2">
        <div>
          <dt className="mono-label">Latency</dt>
          <dd className="font-mono text-xs text-ink">{worker.latencyMs} ms</dd>
        </div>
        <div>
          <dt className="mono-label">In-flight</dt>
          <dd className="font-mono text-xs text-ink">{formatNumber(worker.inFlight)}</dd>
        </div>
        <div>
          <dt className="mono-label">CPU</dt>
          <dd className="font-mono text-xs text-ink">{worker.cpuPct}%</dd>
        </div>
        <div>
          <dt className="mono-label">GPU</dt>
          <dd className="font-mono text-xs text-ink">
            {worker.gpuPct === null ? <span className="text-dim">n/a</span> : `${worker.gpuPct}%`}
          </dd>
        </div>
        <div>
          <dt className="mono-label">Served</dt>
          <dd className="font-mono text-xs text-ink">{formatNumber(worker.served)}</dd>
        </div>
        <div>
          <dt className="mono-label">Last score</dt>
          <dd className="font-mono text-xs text-ink">
            {candidate ? scoreString(candidate) : '—'}
          </dd>
        </div>
      </dl>

      {worker.lastError ? (
        <p className="mt-2 font-mono text-[10px] text-bad">{worker.lastError}</p>
      ) : null}

      <div className="mt-3 flex flex-wrap gap-1.5 border-t border-line pt-3">
        <Button variant="secondary" size="sm" onClick={onLoad} disabled={unavailable}>
          Load
        </Button>
        {unavailable ? (
          <Button variant="success" size="sm" onClick={onRevive}>
            Revive
          </Button>
        ) : (
          <Button variant="danger" size="sm" onClick={onKill}>
            Kill
          </Button>
        )}
      </div>
    </div>
  )
}

export default function MLPreview() {
  const ml = useMockML()
  const [model, setModel] = useState('resnet50')
  const [input, setInput] = useState('{"image":"sample.jpg"}')
  const last = ml.decisions[0] ?? null

  const chartData: ScoreDatum[] =
    last?.candidates
      .filter((candidate) => candidate.normalized !== null)
      .map((candidate) => {
        const normalized = candidate.normalized
        return {
          id: candidate.workerId,
          latency: normalized ? ML_WEIGHTS.latency * normalized.latency : 0,
          queue: normalized ? ML_WEIGHTS.queue * normalized.queue : 0,
          gpu: normalized ? ML_WEIGHTS.gpu * normalized.gpu : 0,
          cpu: normalized ? ML_WEIGHTS.cpu * normalized.cpu : 0,
        }
      }) ?? []

  return (
    <div>
      <PageHeader
        eyebrow="ML inference · preview"
        title="ML routing preview"
        description="A self-contained simulation of the planned ML inference router: model first, health filter, then weighted score. Every number on this page is generated in the browser."
        badges={
          <>
            <Badge tone="ml" dot>
              Mock engine
            </Badge>
            <Badge tone="warn">ML inference in progress</Badge>
            <Badge tone="neutral">No network calls</Badge>
          </>
        }
        actions={
          <Button variant="secondary" onClick={ml.reset}>
            <Icon name="refresh" className="h-3.5 w-3.5" />
            Reset mock
          </Button>
        }
      />

      <div className="relative mb-5 flex flex-col gap-3 rounded-none border border-ml border-dashed bg-mock-soft px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <DashedCorners />
        <MicroTextBlock
          className="absolute right-28 top-1/2 hidden w-16 -translate-y-1/2 text-ml opacity-60 lg:flex"
          lines={[82, 55, 68]}
        />
        <div className="flex items-start gap-3">
          <Icon name="shield" className="mt-0.5 h-4 w-4 shrink-0 text-ml" />
          <div>
            <p className="text-xs font-medium text-ink">Mock — not connected to Meridian</p>
            <p className="mt-1 text-[11px] leading-relaxed text-dim">
              This page never calls the backend, internal/ml, mlworker or mltui. It demonstrates the
              documented routing algorithm with deterministic, simulated worker metrics.
            </p>
          </div>
        </div>
        <Badge tone="ml">Mock</Badge>
      </div>

      <div className="grid gap-4 xl:grid-cols-[0.82fr_1.18fr]">
        <div className="space-y-4">
          <Panel
            title="Send mock request"
            subtitle="The input is display-only; the mock router does not inspect it."
          >
            <div className="space-y-4">
              <div>
                <label htmlFor="ml-model" className="mono-label mb-1.5 block">
                  Model
                </label>
                <input
                  id="ml-model"
                  className="input font-mono"
                  list="ml-model-options"
                  value={model}
                  onChange={(event) => setModel(event.target.value)}
                  spellCheck={false}
                  autoComplete="off"
                />
                <datalist id="ml-model-options">
                  {ml.models.map((item) => (
                    <option key={item} value={item} />
                  ))}
                </datalist>
                <div className="mt-2 flex flex-wrap gap-1.5">
                  {ml.models.map((item) => (
                    <button
                      key={item}
                      type="button"
                      onClick={() => setModel(item)}
                      className={`rounded-none border px-2 py-1 font-mono text-[10px] transition-colors ${
                        model === item
                          ? 'border-accent bg-accent-soft text-ink'
                          : 'border-line bg-panel text-dim hover:text-ink'
                      }`}
                    >
                      {item}
                    </button>
                  ))}
                </div>
                <p className="mt-2 text-[11px] text-dim">
                  Type any other model to see the 404 path.
                </p>
              </div>

              <div>
                <label htmlFor="ml-input" className="mono-label mb-1.5 block">
                  Input
                </label>
                <textarea
                  id="ml-input"
                  className="textarea"
                  rows={3}
                  value={input}
                  onChange={(event) => setInput(event.target.value)}
                  spellCheck={false}
                />
              </div>

              <Button variant="primary" onClick={() => ml.sendRequest(model.trim())}>
                <Icon name="play" className="h-4 w-4" />
                Route mock request
              </Button>
            </div>
          </Panel>

          <Panel title="Scoring weights" subtitle="Constants in mock/ml/weights.ts. Lowest score wins.">
            <div className="space-y-3">
              {weights.map((weight) => (
                <div key={weight.key}>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-xs text-ink">{weight.label}</span>
                    <span className="font-mono text-[11px] text-dim">
                      {weight.value.toFixed(2)}
                    </span>
                  </div>
                  <div className="mt-1.5 h-[3px] overflow-hidden rounded-none bg-black/10">
                    <div
                      className="h-full rounded-none"
                      style={{ width: `${weight.value * 100}%`, background: weight.tone }}
                    />
                  </div>
                </div>
              ))}
              <div className="rounded-none border border-line bg-panel-alt px-3 py-2 font-mono text-[11px] leading-relaxed text-ink">
                score = 0.40·latency + 0.25·queue + 0.20·gpu + 0.15·cpu
              </div>
              <p className="text-[11px] leading-relaxed text-dim">
                Metrics normalize to [0,1] across healthy candidates. GPU is n/a on CPU-only
                workers, so its GPU term is 0. Unhealthy workers are removed before scoring and are
                never selected. High load does not imply unhealthy.
              </p>
            </div>
          </Panel>
        </div>

        <Panel
          title="Mock workers"
          subtitle="State, load and metrics are simulated. Load raises metrics without changing health."
          actions={<Badge tone="ml">Mock</Badge>}
          bodyClassName="relative p-5"
        >
          <DashedCorners />
          <div className="grid gap-3 sm:grid-cols-2">
            {ml.workers.map((worker) => (
              <WorkerCard
                key={worker.id}
                worker={worker}
                decision={last}
                onLoad={() => ml.load(worker.id)}
                onKill={() => ml.kill(worker.id)}
                onRevive={() => ml.revive(worker.id)}
              />
            ))}
          </div>
          <p className="mt-4 border-t border-line pt-3 text-[11px] leading-relaxed text-dim">
            Try it: load one worker, then route a request and watch the scores shift. Kill every
            worker for a model to produce a 503. Revive returns a worker through{' '}
            <span className="text-warn">starting</span> to{' '}
            <span className="text-ok">healthy</span>.
          </p>
        </Panel>
      </div>

      <Panel
        className="mt-4"
        title="Last routing decision"
        subtitle={
          last
            ? `${decisionTime(last.at)} · model=${last.model} · ${last.message}`
            : 'Send a mock request to inspect candidates and scores.'
        }
        actions={
          last ? (
            <div className="flex items-center gap-2">
              <Badge tone={statusTone(last.status)}>{last.status}</Badge>
              <Badge tone="ml">Mock</Badge>
            </div>
          ) : undefined
        }
        bodyClassName="p-0"
      >
          {last ? (
            last.candidates.length > 0 ? (
              <div className="overflow-x-auto">
                <table className="data-table">
                  <caption className="sr-only">
                    Mock routing candidates for the last {last.model} request
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Worker</th>
                      <th scope="col">State</th>
                      <th scope="col">Latency</th>
                      <th scope="col">In-flight</th>
                      <th scope="col">CPU</th>
                      <th scope="col">GPU</th>
                      <th scope="col" title="Normalized latency">nLat</th>
                      <th scope="col" title="Normalized queue">nQ</th>
                      <th scope="col" title="Normalized GPU">nGPU</th>
                      <th scope="col" title="Normalized CPU">nCPU</th>
                      <th scope="col">Score</th>
                      <th scope="col">Result</th>
                    </tr>
                  </thead>
                  <tbody>
                    {last.candidates.map((candidate) => (
                      <tr
                        key={candidate.workerId}
                        className={candidate.selected ? 'bg-accent-soft' : ''}
                      >
                        <td className="font-mono text-xs text-ink">{candidate.workerId}</td>
                        <td>
                          <StatusDot state={candidate.state} />
                        </td>
                        <td className="font-mono text-xs text-ink">{candidate.raw.latencyMs} ms</td>
                        <td className="font-mono text-xs text-ink">
                          {formatNumber(candidate.raw.inFlight)}
                        </td>
                        <td className="font-mono text-xs text-ink">{candidate.raw.cpuPct}%</td>
                        <td className="font-mono text-xs text-ink">
                          {candidate.raw.gpuPct === null ? (
                            <span className="text-dim">n/a</span>
                          ) : (
                            `${candidate.raw.gpuPct}%`
                          )}
                        </td>
                        <td className="font-mono text-[11px] text-dim">
                          {normString(candidate.normalized?.latency)}
                        </td>
                        <td className="font-mono text-[11px] text-dim">
                          {normString(candidate.normalized?.queue)}
                        </td>
                        <td className="font-mono text-[11px] text-dim">
                          {normString(candidate.normalized?.gpu)}
                        </td>
                        <td className="font-mono text-[11px] text-dim">
                          {normString(candidate.normalized?.cpu)}
                        </td>
                        <td className="font-mono text-xs font-semibold text-ink">
                          {scoreString(candidate)}
                        </td>
                        <td>
                          {candidate.selected ? (
                            <Badge tone="ml">selected</Badge>
                          ) : candidate.score === null ? (
                            <Badge tone="bad">excluded</Badge>
                          ) : (
                            <span className="text-[11px] text-dim">candidate</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {last.status !== 200 ? (
                  <p className="border-t border-line px-5 py-3 font-mono text-[11px] text-warn">
                    {last.message}
                  </p>
                ) : null}
              </div>
            ) : (
              <div className="p-4">
                <EmptyState
                  icon="alert"
                  title={`${last.status} — no candidates`}
                  message={last.message}
                />
              </div>
            )
          ) : (
            <div className="p-4">
              <EmptyState
                icon="ml"
                title="No routing decisions yet"
                message="Route a mock request to see candidates, raw metrics, normalized scores, the final score and the chosen worker."
              />
            </div>
          )}
      </Panel>

      <div className="mt-4 grid gap-4 xl:grid-cols-[1.35fr_0.65fr]">
        <Panel
          title="Routing log"
          subtitle="Recent mock decisions, newest first. Values are simulated."
          actions={
            <Badge tone="neutral">
              {ml.decisions.length} {ml.decisions.length === 1 ? 'entry' : 'entries'}
            </Badge>
          }
        >
          {ml.decisions.length > 0 ? (
            <ol className="space-y-2">
              {ml.decisions.map((decision) => (
                <li
                  key={decision.id}
                  className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-none border border-line bg-panel px-3 py-2 font-mono text-[11px]"
                >
                  <span className="text-dim">{decisionTime(decision.at)}</span>
                  <Badge tone={statusTone(decision.status)}>{decision.status}</Badge>
                  <span className="text-ink">{decision.model}</span>
                  <span className="text-dim">
                    {decision.chosen ? `→ ${decision.chosen}` : '→ no worker'} · {decision.message}
                  </span>
                </li>
              ))}
            </ol>
          ) : (
            <p className="text-xs text-dim">No decisions recorded yet.</p>
          )}
        </Panel>

        <Panel
          title="Score composition"
          subtitle="Weighted contributions of the last decision's healthy candidates."
          actions={<Badge tone="ml">Mock</Badge>}
        >
          {chartData.length > 0 ? (
            <ScoreBreakdown data={chartData} />
          ) : (
            <EmptyState
              icon="layers"
              title="No scored candidates"
              message="A successful mock request populates this chart."
            />
          )}
        </Panel>
      </div>
    </div>
  )
}
