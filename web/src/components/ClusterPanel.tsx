import { fetchClusterStatus } from '../api'
import { usePoll } from '../hooks/usePoll'
import type { CatalogModel, WorkerStatus } from '../types'

interface Props {
  models: CatalogModel[]
}

// ClusterPanel shows every llama-server worker grouped by model, refreshed
// every two seconds from /status. It is the view used to demonstrate failover:
// kill a worker and its card turns unhealthy while the others keep serving.
export function ClusterPanel({ models }: Props) {
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
  const maxTok = Math.max(1, ...workers.map((w) => w.tok_per_sec))
  const groups = groupByModel(workers)

  return (
    <section className="panel cluster">
      <div className="panel-head">
        <h2>Cluster</h2>
        {error && <span className="badge badge-warn" title={error}>stale</span>}
      </div>

      <div className="summary">
        <Stat label="healthy" value={`${healthy}/${workers.length}`} />
        <Stat label="sessions" value={String(data.sessionCount)} />
        <Stat label="models" value={String(groups.length)} />
      </div>

      {workers.length === 0 && (
        <p className="muted">No LLM workers. Start the server with <code>--llm &lt;file.gguf&gt;[:N]</code>.</p>
      )}

      {groups.map(([model, list]) => {
        const info = models.find((m) => m.id === model)
        return (
          <div className="model-group" key={model}>
            <div className="model-title">
              <span className="model-name" title={model}>{model}</span>
              <span className="muted small">
                {list.filter((w) => w.healthy).length}/{list.length} up
              </span>
            </div>
            {info?.description && <p className="muted small">{info.description}</p>}
            {list.map((w) => (
              <WorkerCard key={w.id} worker={w} maxTok={maxTok} />
            ))}
          </div>
        )
      })}
    </section>
  )
}

function WorkerCard({ worker: w, maxTok }: { worker: WorkerStatus; maxTok: number }) {
  const pct = Math.min(100, (w.tok_per_sec / maxTok) * 100)
  return (
    <div className={`worker worker-${w.state}`}>
      <div className="worker-head">
        <span className="dot" />
        <strong>{w.id}</strong>
        <span className="muted small">:{w.port}</span>
        <span className={`badge badge-${w.state}`}>{w.state}</span>
      </div>

      <div className="bar" title={`${w.tok_per_sec.toFixed(1)} tok/s`}>
        <div className="bar-fill" style={{ width: `${pct}%` }} />
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

function groupByModel(workers: WorkerStatus[]): [string, WorkerStatus[]][] {
  const map = new Map<string, WorkerStatus[]>()
  for (const w of workers) {
    const list = map.get(w.model) ?? []
    list.push(w)
    map.set(w.model, list)
  }
  return [...map.entries()]
}
