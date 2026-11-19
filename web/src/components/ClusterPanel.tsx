import { fetchClusterStatus } from '../api'
import { usePoll } from '../hooks/usePoll'
import type { CatalogModel, WorkerStatus } from '../types'

interface Props {
  models: CatalogModel[]
  policy: string
}

// Reference request for the predicted finish time. It matches the server's
// default output estimate, so the numbers mirror what token-aware routing scores.
const REF_INPUT = 100
const REF_OUTPUT = 128
const REF_AVG_OUTPUT = 128

// predictSeconds is the token-aware score C = (F*(I+Obar) + I + O) / R.
// Returns null when the worker has no usable throughput.
function predictSeconds(w: WorkerStatus): number | null {
  if (!(w.tok_per_sec > 0)) return null
  return (w.in_flight * (REF_INPUT + REF_AVG_OUTPUT) + REF_INPUT + REF_OUTPUT) / w.tok_per_sec
}

// ClusterPanel shows every llama-server worker grouped by model, refreshed
// every two seconds from /status. Each card also shows the predicted finish
// time for a reference request; under token-aware routing the lowest one is
// marked as the next pick.
export function ClusterPanel({ models, policy }: Props) {
  const { data, error } = usePoll(fetchClusterStatus, 2000)

  if (!data && !error) return <section className="panel cluster"><p className="muted">Connecting…</p></section>
  if (!data && error) {
    return (
      <section className="panel cluster">
        <h2>Cluster</h2>
        <p className="error-line">Cannot reach the gateway: {error}</p>
      </section>
    )
  }
  if (!data) return null

  const workers = data.workers
  const healthy = workers.filter((w) => w.healthy).length
  const groups = groupByModel(workers)
  const predictive = policy === 'token-aware'

  return (
    <section className="panel cluster">
      <div className="panel-head">
        <h2>Cluster</h2>
        <div className="head-badges">
          <span className="badge badge-policy" title="Default routing policy">{policy || '—'}</span>
          {error && <span className="badge badge-warn" title={error}>stale</span>}
        </div>
      </div>

      <div className="summary">
        <Stat label="healthy" value={`${healthy}/${workers.length}`} />
        <Stat label="sessions" value={String(data.sessionCount)} />
        <Stat label="models" value={String(groups.length)} />
      </div>

      <p className="policy-line">
        {predictive
          ? `Predicted finish for a ${REF_INPUT}→${REF_OUTPUT} token request. The lowest one is the next pick.`
          : `Predicted finish for a ${REF_INPUT}→${REF_OUTPUT} token request, shown for reference. Routing uses ${policy || 'the default policy'}.`}
      </p>

      {workers.length === 0 && (
        <p className="muted">No LLM workers. Start the server with <code>--llm &lt;file.gguf&gt;[:N]</code>.</p>
      )}

      {groups.map(([model, list]) => {
        const info = models.find((m) => m.id === model)
        const predictions = list.map((w) => (w.healthy ? predictSeconds(w) : null))
        const maxPred = Math.max(1, ...predictions.map((p) => p ?? 0))
        const bestIdx = predictive ? indexOfMin(predictions) : -1
        return (
          <div className="model-group" key={model}>
            <div className="model-title">
              <span className="model-name" title={model}>{model}</span>
              <span className="muted small">
                {list.filter((w) => w.healthy).length}/{list.length} up
              </span>
            </div>
            {info?.description && <p className="muted small">{info.description}</p>}
            {list.map((w, i) => (
              <WorkerCard
                key={w.id}
                worker={w}
                predicted={predictions[i]}
                maxPredicted={maxPred}
                isNext={i === bestIdx}
              />
            ))}
          </div>
        )
      })}
    </section>
  )
}

function WorkerCard({
  worker: w,
  predicted,
  maxPredicted,
  isNext,
}: {
  worker: WorkerStatus
  predicted: number | null
  maxPredicted: number
  isNext: boolean
}) {
  const pct = predicted === null ? 0 : Math.min(100, (predicted / maxPredicted) * 100)
  return (
    <div className={`worker worker-${w.state}${isNext ? ' worker-next' : ''}`}>
      <div className="worker-head">
        <span className="dot" />
        <strong>{w.id}</strong>
        <span className="muted small">:{w.port}</span>
        {isNext && <span className="badge badge-next">next</span>}
        <span className={`badge badge-${w.state}`}>{w.state}</span>
      </div>

      <div className="predict">
        <span className="muted small">predicted finish</span>
        <span className="predict-value">{predicted === null ? '—' : `${predicted.toFixed(1)} s`}</span>
      </div>
      <div className="bar predict-bar" title="Predicted finish time, shorter is better">
        <div className="bar-fill predict-fill" style={{ width: `${pct}%` }} />
      </div>

      <div className="bar" title={`${w.tok_per_sec.toFixed(1)} tok/s`}>
        <div className="bar-fill" style={{ width: `${Math.min(100, (w.tok_per_sec / 80) * 100)}%` }} />
      </div>

      <dl className="metrics">
        <Metric label="tok/s" value={w.tok_per_sec.toFixed(1)} />
        <Metric label="in flight" value={String(w.in_flight)} />
        <Metric label="served" value={String(w.served)} />
        <Metric label="sessions" value={String(w.sessions)} />
        <Metric label="est. tokens" value={String(w.est_tokens_in_flight)} />
        <Metric label="failures" value={String(w.consecutive_failures)} />
      </dl>

      {w.last_error && <div className="error-line">{w.last_error}</div>}
      <div className="muted small">
        pid {w.pid || '—'}
        {w.last_check && ` · checked ${new Date(w.last_check).toLocaleTimeString()}`}
      </div>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="metric">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  )
}

// indexOfMin returns the index of the smallest non-null prediction, or -1.
function indexOfMin(values: (number | null)[]): number {
  let best = -1
  let bestVal = Infinity
  values.forEach((v, i) => {
    if (v !== null && v < bestVal) {
      best = i
      bestVal = v
    }
  })
  return best
}

function groupByModel(workers: WorkerStatus[]): [string, WorkerStatus[]][] {
  const map = new Map<string, WorkerStatus[]>()
  for (const w of workers) {
    const list = map.get(w.model) ?? []
    list.push(w)
    map.set(w.model, list)
  }
  return [...map.entries()]
}
