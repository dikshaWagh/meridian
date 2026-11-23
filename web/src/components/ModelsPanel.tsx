import type { CatalogModel } from '../types'

interface Props {
  models: CatalogModel[]
  policy: string
}

// ModelsPanel is the user-facing side panel. It shows which models are
// available and how many of their instances are up. It never shows machines,
// addresses or process IDs; those are for admins only.
export function ModelsPanel({ models, policy }: Props) {
  return (
    <aside className="models-panel">
      <div className="card">
        <div className="card-label">Models</div>
        {models.length === 0 && <p className="muted small">No models are available yet.</p>}
        <ul className="model-list">
          {models.map((m) => {
            const up = m.healthy_workers ?? 0
            const total = m.workers ?? 0
            const state = up > 0 ? 'ready' : m.state === 'starting' ? 'starting' : 'down'
            return (
              <li key={m.id} className="model-row">
                <span className={`status-dot status-${state}`} title={state} />
                <div className="model-meta">
                  <div className="model-name">{m.id}</div>
                  <div className="muted small">
                    {up}/{total} instances ready{m.context_window ? ` · ${m.context_window} ctx` : ''}
                  </div>
                </div>
              </li>
            )
          })}
        </ul>
      </div>
      <div className="card routing-note">
        <div className="card-label">Routing</div>
        <p className="small">
          Requests are sent automatically to the best available instance. The system policy is <strong>{policy || '—'}</strong>{' '}
          and is set by an admin.
        </p>
      </div>
    </aside>
  )
}
