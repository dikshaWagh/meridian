import { useMemo } from 'react'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Icon } from '../components/ui/Icon'
import { Panel } from '../components/ui/Panel'
import { Stat } from '../components/ui/Stat'
import { StatusDot } from '../components/ui/StatusDot'
import { EmptyState, LoadingBlock } from '../components/ui/States'
import { PageHeader } from '../components/layout/PageHeader'
import { ModeBadge } from '../components/layout/ModeBadge'
import { ObservedChart } from '../components/viz/ObservedChart'
import { useMeridianStatus } from '../hooks/useMeridianStatus'
import { useObservedHistory } from '../hooks/useObservedHistory'
import { useSimulation } from '../hooks/useSimulation'
import { useMode } from '../lib/useMode'
import { formatNumber, formatRelativeTime, formatIsoTime, truncate } from '../lib/formatters'
import type { WorkerStatus } from '../lib/types'

export default function Dashboard() {
  const { mode } = useMode()
  const isLive = mode === 'live'
  const live = useMeridianStatus(isLive)
  const sim = useSimulation(!isLive)

  const workers = isLive ? live.workers : sim.workers
  const models = isLive ? live.models : sim.models
  const loading = isLive && live.loading
  const error = isLive ? live.error : null
  const offline = isLive && live.offline
  const lastUpdated = isLive ? live.lastUpdated : null

  const grouped = useMemo(() => {
    const map = new Map<string, WorkerStatus[]>()
    for (const worker of workers) {
      const list = map.get(worker.model) ?? []
      list.push(worker)
      map.set(worker.model, list)
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b))
  }, [workers])

  const totals = useMemo(() => {
    return workers.reduce(
      (acc, worker) => {
        acc.total += 1
        if (worker.state === 'healthy') acc.healthy += 1
        if (worker.state === 'starting') acc.starting += 1
        if (worker.state === 'unhealthy') acc.unhealthy += 1
        acc.inFlight += worker.inFlight
        acc.served += worker.served
        acc.tokens += worker.estTokensInFlight
        return acc
      },
      { total: 0, healthy: 0, starting: 0, unhealthy: 0, inFlight: 0, served: 0, tokens: 0 },
    )
  }, [workers])

  const history = useObservedHistory(workers, workers.length > 0)
  const hasWorkers = workers.length > 0

  return (
    <div>
      <PageHeader
        eyebrow="LLM balancer · telemetry"
        title="Worker dashboard"
        description={
          isLive
            ? 'Live worker state from GET /status, polled every 2 seconds while this tab is visible. Health is probed by the backend every 2 seconds.'
            : 'A local mirror of the verified balancer: per-model round robin, 2 s probes, 3 consecutive failures to unhealthy, 180 s starting grace. No backend calls.'
        }
        badges={
          <>
            <ModeBadge />
            <Badge tone="neutral">{isLive ? 'GET /status · 2 s' : 'Local mirror'}</Badge>
            {isLive && lastUpdated ? (
              <span className="text-[11px] text-dim" role="status" aria-live="polite">
                Updated {formatRelativeTime(new Date(lastUpdated).toISOString())}
              </span>
            ) : null}
          </>
        }
        actions={
          isLive ? (
            <Button variant="secondary" onClick={live.refresh}>
              <Icon name="refresh" className="h-3.5 w-3.5" />
              Refresh
            </Button>
          ) : (
            <Button variant="secondary" onClick={sim.reset}>
              <Icon name="refresh" className="h-3.5 w-3.5" />
              Reset simulation
            </Button>
          )
        }
      />

      {!isLive ? (
        <div className="mb-5 flex flex-col gap-3 rounded-none border border-warn bg-warn-soft px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <Icon name="alert" className="mt-0.5 h-4 w-4 shrink-0 text-warn" />
            <div>
              <p className="text-xs font-medium text-ink">Simulated data — not backend telemetry</p>
              <p className="mt-1 text-[11px] leading-relaxed text-dim">
                Kill marks a process as exited immediately. “Fail probes” makes /health fail; after
                three consecutive failing probes the worker becomes unhealthy. Revive returns it to
                starting, then healthy on the next successful probe.
              </p>
            </div>
          </div>
          <Badge tone="warn">Simulated</Badge>
        </div>
      ) : null}

      {offline ? (
        <div
          role="alert"
          className="mb-5 flex flex-col gap-3 rounded-none border border-bad bg-bad-soft px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
        >
          <div className="flex items-start gap-3">
            <Icon name="alert" className="mt-0.5 h-4 w-4 shrink-0 text-bad" />
            <div>
              <p className="text-xs font-medium text-ink">Backend offline</p>
              <p className="mt-1 text-[11px] leading-relaxed text-dim">{error}</p>
              {hasWorkers ? (
                <p className="mt-1 text-[11px] text-warn">
                  Showing the last known snapshot; values may be stale.
                </p>
              ) : null}
            </div>
          </div>
          <Button variant="secondary" size="sm" onClick={live.refresh}>
            <Icon name="refresh" className="h-3.5 w-3.5" />
            Retry
          </Button>
        </div>
      ) : null}

      {!offline && error ? (
        <div
          role="alert"
          className="mb-5 rounded-none border border-bad bg-bad-soft px-4 py-3"
        >
          <p className="text-xs font-medium text-ink">Could not load worker status</p>
          <p className="mt-1 text-[11px] text-dim">{error}</p>
        </div>
      ) : null}

      {loading && !hasWorkers ? (
        <Panel title="Connecting to Meridian" subtitle="Waiting for the first GET /status response.">
          <LoadingBlock label="Loading worker status" />
        </Panel>
      ) : !hasWorkers ? (
        <Panel>
          <EmptyState
            icon="server"
            title={offline ? 'No worker snapshot available' : 'No workers reported'}
            message={
              offline
                ? 'Start Meridian, then refresh. In mock mode the supervisor spawns four simulated llama-server processes.'
                : 'GET /status returned an empty worker list. Check that the Meridian balancer is running.'
            }
            action={
              isLive ? (
                <Button variant="secondary" size="sm" onClick={live.refresh}>
                  <Icon name="refresh" className="h-3.5 w-3.5" />
                  Retry now
                </Button>
              ) : undefined
            }
          />
        </Panel>
      ) : (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
            <Stat
              label="Healthy workers"
              value={`${totals.healthy} / ${totals.total}`}
              hint={`${totals.starting} starting · ${totals.unhealthy} unhealthy`}
              icon="shield"
              accent={totals.unhealthy > 0 ? 'warn' : 'ok'}
            />
            <Stat
              label="In-flight"
              value={formatNumber(totals.inFlight)}
              hint="Requests currently proxied"
              icon="activity"
              accent="cyan"
            />
            <Stat
              label="Served"
              value={formatNumber(totals.served)}
              hint="Responses counted by workers (status < 500)"
              icon="bolt"
              accent="cyan"
            />
            <Stat
              label="Est. tokens in flight"
              value={formatNumber(totals.tokens)}
              hint="Estimated from request shapes while proxying"
              icon="layers"
              accent="neutral"
            />
          </div>

          <div className="grid gap-4 xl:grid-cols-[1.45fr_1fr]">
            <Panel
              title="In-flight over time"
              subtitle={
                isLive
                  ? 'Client-observed samples from the status feed. Last 30 polls.'
                  : 'Client-observed samples from the simulated feed. Last 30 ticks.'
              }
              actions={
                <Badge tone="neutral">
                  {history.length} {history.length === 1 ? 'sample' : 'samples'}
                </Badge>
              }
            >
              {history.length > 1 ? (
                <ObservedChart data={history} />
              ) : (
                <EmptyState
                  icon="activity"
                  title="Collecting samples"
                  message="The chart fills as status updates arrive."
                />
              )}
            </Panel>

            <Panel
              title="Models"
              subtitle={
                isLive
                  ? 'GET /v1/models lists only models with at least one healthy worker.'
                  : 'Derived locally with the same rule: only models with a healthy worker.'
              }
            >
              {models.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {models.map((model) => (
                    <span
                      key={model}
                      className="rounded-none border border-line bg-panel-alt px-2.5 py-1.5 font-mono text-xs text-ink"
                    >
                      {model}
                    </span>
                  ))}
                </div>
              ) : (
                <p className="text-xs leading-relaxed text-dim">
                  No models are currently advertised. A model disappears from this list when none of
                  its workers are healthy.
                </p>
              )}

              <div className="mt-5 space-y-2 border-t border-line pt-4">
                <p className="mono-label">State legend</p>
                <div className="flex flex-wrap gap-4">
                  <StatusDot state="healthy" />
                  <StatusDot state="starting" />
                  <StatusDot state="unhealthy" />
                </div>
                <p className="pt-1 text-[11px] leading-relaxed text-dim">
                  Healthy = can receive traffic. Starting = inside the 180 s readiness grace period.
                  Unhealthy = not selected. High in-flight load does not change health.
                </p>
              </div>
            </Panel>
          </div>

          {grouped.map(([model, list]) => {
            const healthy = list.filter((worker) => worker.state === 'healthy').length
            return (
              <Panel
                key={model}
                title={
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-sm normal-case tracking-normal text-ink">{model}</span>
                    <Badge tone={healthy > 0 ? 'accent' : 'bad'}>
                      {healthy}/{list.length} healthy
                    </Badge>
                  </span>
                }
                subtitle="Per-model group. Round robin only advances across this model's healthy workers."
                bodyClassName="p-0"
              >
                <div className="overflow-x-auto">
                  <table className="data-table">
                    <caption className="sr-only">Workers serving {model}</caption>
                    <thead>
                      <tr>
                        <th scope="col">Worker</th>
                        <th scope="col">State</th>
                        <th scope="col">In-flight</th>
                        <th scope="col">Served</th>
                        <th scope="col">Est. tokens</th>
                        <th scope="col">Consec. failures</th>
                        <th scope="col">Last check</th>
                        <th scope="col">Last error</th>
                        {!isLive ? <th scope="col">Simulate</th> : null}
                      </tr>
                    </thead>
                    <tbody>
                      {list.map((worker) => (
                        <tr key={worker.id}>
                          <td>
                            <div className="font-mono text-xs text-ink">{worker.id}</div>
                            <div className="mt-0.5 font-mono text-[10px] text-dim">
                              :{worker.port} · pid {worker.pid || '—'}
                            </div>
                          </td>
                          <td>
                            <StatusDot state={worker.state} />
                            {worker.state === 'starting' ? (
                              <div className="mt-1 font-mono text-[10px] text-dim">grace 180 s</div>
                            ) : null}
                          </td>
                          <td className="font-mono text-xs text-ink">{formatNumber(worker.inFlight)}</td>
                          <td className="font-mono text-xs text-ink">{formatNumber(worker.served)}</td>
                          <td className="font-mono text-xs text-ink">
                            {formatNumber(worker.estTokensInFlight)}
                          </td>
                          <td>
                            <span
                              className={`font-mono text-xs ${
                                worker.consecutiveFailures > 0 ? 'text-warn' : 'text-dim'
                              }`}
                            >
                              {worker.consecutiveFailures} / 3
                            </span>
                          </td>
                          <td
                            className="font-mono text-[11px] text-dim"
                            title={worker.lastCheck || 'No probe yet'}
                          >
                            {formatIsoTime(worker.lastCheck)}
                          </td>
                          <td className="max-w-[220px]">
                            {worker.lastError ? (
                              <span className="text-[11px] text-bad" title={worker.lastError}>
                                {truncate(worker.lastError, 48)}
                              </span>
                            ) : (
                              <span className="text-[11px] text-dim">—</span>
                            )}
                          </td>
                          {!isLive ? (
                            <td>
                              <div className="flex flex-wrap gap-1.5">
                                {worker.state === 'healthy' ? (
                                  <Button
                                    variant="danger"
                                    size="sm"
                                    onClick={() => sim.kill(worker.id)}
                                  >
                                    Kill
                                  </Button>
                                ) : (
                                  <Button
                                    variant="success"
                                    size="sm"
                                    disabled={worker.state === 'starting'}
                                    onClick={() => sim.revive(worker.id)}
                                  >
                                    {worker.state === 'starting' ? 'Starting…' : 'Revive'}
                                  </Button>
                                )}
                                <Button
                                  variant="secondary"
                                  size="sm"
                                  disabled={worker.state !== 'healthy'}
                                  onClick={() => sim.toggleHealthFailing(worker.id)}
                                >
                                  {worker.healthFailing ? 'Stop failing' : 'Fail probes'}
                                </Button>
                              </div>
                            </td>
                          ) : null}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {!isLive ? (
                  <p className="border-t border-line px-5 py-3 text-[11px] leading-relaxed text-dim">
                    Simulated state machine. “Kill” mirrors a crashed process (immediately
                    unhealthy). There is no automatic restart, matching the real supervisor.
                  </p>
                ) : null}
              </Panel>
            )
          })}

          <p className="text-[11px] leading-relaxed text-dim">
            {isLive ? (
              <>
                Live view: served counts and estimated token totals come directly from GET /status.
                Latency, CPU and GPU metrics are not exposed by the LLM balancer and are not shown
                here.
              </>
            ) : (
              <>Simulation only: served counts, in-flight values and estimates are local state.</>
            )}
          </p>
        </div>
      )}

      {isLive && live.error === null && lastUpdated ? (
        <p className="sr-only" role="status" aria-live="polite">
          Worker status updated.
        </p>
      ) : null}
    </div>
  )
}
