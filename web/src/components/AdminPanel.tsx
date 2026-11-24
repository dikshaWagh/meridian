import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError, errorMessage, fetchAdminCluster, setSystemPolicy, streamAdminEvents } from '../api'
import { useCountUp } from '../hooks/useCountUp'
import type { AdminCluster, RouteEvent } from '../types'
import { ClusterGraph, litKeys } from './ClusterGraph'

const LIT_MS = 1600
const EVENT_LOG = 40

interface Props {
  token: string
  onExpired: () => void
}

// AdminPanel is the control panel. It is the only place that shows IP
// addresses, process IDs and the system routing policy, and it is the only
// place the policy can be changed.
export function AdminPanel({ token, onExpired }: Props) {
  const [cluster, setCluster] = useState<AdminCluster | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [events, setEvents] = useState<RouteEvent[]>([])
  const [lit, setLit] = useState<Record<string, number>>({})
  const [now, setNow] = useState(() => Date.now())

  const handleError = useCallback(
    (err: unknown) => {
      if (err instanceof ApiError && err.status === 401) {
        onExpired()
        return
      }
      setError(errorMessage(err))
    },
    [onExpired],
  )

  // Cluster state: poll every 2 s.
  useEffect(() => {
    let alive = true
    const tick = async () => {
      try {
        const c = await fetchAdminCluster(token)
        if (alive) {
          setCluster(c)
          setError(null)
        }
      } catch (err) {
        if (alive) handleError(err)
      }
    }
    void tick()
    const timer = setInterval(tick, 2000)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [token, handleError])

  // Route events: keep a stream open and reconnect after a drop.
  useEffect(() => {
    const ctrl = new AbortController()
    const onEvent = (e: RouteEvent) => {
      setEvents((prev) => [e, ...prev].slice(0, EVENT_LOG))
      const until = Date.now() + LIT_MS
      setLit((prev) => {
        const next = { ...prev }
        for (const k of litKeys(e.node_id, e.replica_id)) next[k] = until
        return next
      })
    }
    const run = async () => {
      while (!ctrl.signal.aborted) {
        try {
          await streamAdminEvents(token, onEvent, ctrl.signal)
        } catch (err) {
          if (ctrl.signal.aborted) return
          if (err instanceof ApiError && err.status === 401) {
            onExpired()
            return
          }
        }
        await new Promise((r) => setTimeout(r, 2000))
      }
    }
    void run()
    return () => ctrl.abort()
  }, [token, onExpired])

  // Drive the highlight fade and prune expired entries.
  useEffect(() => {
    const timer = setInterval(() => {
      const t = Date.now()
      setNow(t)
      setLit((prev) => {
        const live = Object.entries(prev).filter(([, until]) => until > t)
        return live.length === Object.keys(prev).length ? prev : Object.fromEntries(live)
      })
    }, 250)
    return () => clearInterval(timer)
  }, [])

  const stats = useMemo(() => {
    if (!cluster) return null
    const replicas = cluster.nodes.flatMap((n) => n.replicas)
    return {
      nodesOnline: cluster.nodes.filter((n) => n.state === 'online').length,
      nodes: cluster.nodes.length,
      ready: replicas.filter((r) => r.healthy).length,
      replicas: replicas.length,
      inFlight: replicas.reduce((s, r) => s + r.in_flight, 0),
      served: replicas.reduce((s, r) => s + r.served, 0),
    }
  }, [cluster])

  const changePolicy = async (name: string) => {
    setSaving(true)
    setNotice(null)
    try {
      await setSystemPolicy(token, name)
      setNotice(`System routing policy is now ${name}.`)
      setCluster(await fetchAdminCluster(token))
    } catch (err) {
      handleError(err)
    } finally {
      setSaving(false)
    }
  }

  if (!cluster) {
    return (
      <section className="admin">
        <p className="muted">{error ? `Cannot reach the leader: ${error}` : 'Connecting to the control panel…'}</p>
      </section>
    )
  }

  const policyInfo = cluster.policies.find((p) => p.name === cluster.policy)

  return (
    <section className="admin">
      <div className="admin-top">
        <div className="card policy-card">
          <div className="card-label">System routing policy</div>
          <p className="muted small policy-note">
            Applies to every request from every user. Only admins can change it.
          </p>
          <select
            className="policy-select"
            value={cluster.policy}
            disabled={saving}
            onChange={(e) => void changePolicy(e.target.value)}
          >
            {cluster.policies.map((p) => (
              <option key={p.name} value={p.name} title={p.description}>
                {p.name}
              </option>
            ))}
          </select>
          {policyInfo && <p key={cluster.policy} className="small policy-desc">{policyInfo.description}</p>}
          {notice && <p className="small ok-line">{notice}</p>}
          {error && <p className="error-line">{error}</p>}
        </div>

        {stats && (
          <div className="stat-grid">
            <Stat label="Nodes online" value={`${stats.nodesOnline}/${stats.nodes}`} />
            <Stat label="Instances ready" value={`${stats.ready}/${stats.replicas}`} />
            <Stat label="In flight" value={String(stats.inFlight)} live={stats.inFlight > 0} />
            <Stat label="Served" value={String(stats.served)} />
          </div>
        )}
      </div>

      <div className="admin-main">
        <div className="card graph-card">
          <div className="card-head">
            <div>
              <div className="card-label">Live topology</div>
              <div className="muted small">
                Leader at the top, workers below. Each request lights the path it took.
              </div>
            </div>
            <div className="legend">
              <span className="legend-item"><i className="sw sw-ready" />ready</span>
              <span className="legend-item"><i className="sw sw-loading" />loading</span>
              <span className="legend-item"><i className="sw sw-bad" />down</span>
              <span className="legend-item"><i className="sw sw-lit" />routing</span>
            </div>
          </div>
          <ClusterGraph cluster={cluster} lit={lit} now={now} />
        </div>

        <div className="card log-card">
          <div className="card-label">Routing events</div>
          {events.length === 0 && (
            <div className="events-empty">
              <span className="radar" aria-hidden="true"><i /><i /><i /></span>
              <p className="muted small">Waiting for requests…</p>
            </div>
          )}
          <ul className="event-log">
            {events.map((e, i) => (
              <li key={`${e.request_id}-${e.phase}-${i}`} className={`ev ev-${e.phase}`}>
                <span className="ev-time">{new Date(e.at).toLocaleTimeString()}</span>
                <span className="ev-phase">{e.phase}</span>
                <span className="ev-path">
                  {e.node_id === cluster.leader_id ? 'leader' : e.node_id.slice(0, 8)} → {e.replica_id}
                </span>
                <span className="ev-model muted">{e.model}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  )
}

function Stat({ label, value, live = false }: { label: string; value: string; live?: boolean }) {
  const parts = value.split('/')
  const fraction = parts.length === 2 && parts.every((p) => /^\d+$/.test(p))
  const numeric = /^\d+$/.test(value) ? Number(value) : 0
  const shown = useCountUp(numeric)
  const ready = fraction ? Number(parts[0]) : 0
  const total = fraction ? Number(parts[1]) : 0

  return (
    <div className={`stat${live ? ' stat-live' : ''}`}>
      <div className="stat-head">
        <span className="stat-label mono">{label}</span>
        <span className="stat-indicator" aria-hidden="true" />
      </div>
      <div className="stat-value mono">{fraction ? value : shown}</div>
      {fraction && total > 0 && total <= 16 ? (
        <div className="stat-ticks" aria-hidden="true">
          {Array.from({ length: total }, (_, i) => (
            <i key={i} className={i < ready ? 'on' : ''} />
          ))}
        </div>
      ) : (
        <div className="stat-rule" aria-hidden="true" />
      )}
    </div>
  )
}
