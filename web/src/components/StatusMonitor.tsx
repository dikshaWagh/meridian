import { useEffect, useState } from 'react'
import { fetchStatus } from '../api'
import type { SystemStatus, WorkerStatus } from '../types'

export function StatusMonitor() {
  const [status, setStatus] = useState<SystemStatus | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const refresh = async () => {
      try {
        const data = await fetchStatus()
        setStatus(data)
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch status')
      }
    }

    refresh()
    const interval = setInterval(refresh, 2000)
    return () => clearInterval(interval)
  }, [])

  if (error) {
    return (
      <div style={styles.container}>
        <div style={styles.header}>Status</div>
        <div style={styles.error}>{error}</div>
      </div>
    )
  }

  if (!status) {
    return (
      <div style={styles.container}>
        <div style={styles.header}>Status</div>
        <div style={styles.loading}>Loading...</div>
      </div>
    )
  }

  return (
    <div style={styles.container}>
      <div style={styles.header}>
        Workers ({status.sessionCount} sessions)
      </div>
      <div style={styles.workersList}>
        {status.workers.map((worker) => (
          <WorkerCard key={worker.id} worker={worker} />
        ))}
      </div>
    </div>
  )
}

function WorkerCard({ worker }: { worker: WorkerStatus }) {
  const isHealthy = worker.healthy
  const healthColor = isHealthy ? '#10b981' : '#ef4444'

  return (
    <div style={styles.workerCard}>
      <div style={styles.workerHeader}>
        <div style={styles.healthDot}>
          <div style={{ ...styles.dot, backgroundColor: healthColor }} />
        </div>
        <div style={styles.workerTitle}>
          {worker.id}
          <span style={styles.state}>{worker.state}</span>
        </div>
      </div>

      <div style={styles.workerDetails}>
        <DetailRow label="Model" value={truncateModel(worker.model)} />
        <DetailRow label="Tok/s" value={worker.tok_per_sec.toFixed(1)} />
        <DetailRow label="Sessions" value={String(worker.sessions)} />
        <DetailRow label="In Flight" value={String(worker.in_flight)} />
        <DetailRow label="Served" value={String(worker.served)} />
        {worker.last_error && (
          <DetailRow label="Error" value={worker.last_error} error />
        )}
      </div>
    </div>
  )
}

function DetailRow({
  label,
  value,
  error,
}: {
  label: string
  value: string
  error?: boolean
}) {
  return (
    <div style={styles.detailRow}>
      <span style={styles.detailLabel}>{label}:</span>
      <span style={{ ...styles.detailValue, color: error ? '#ef4444' : '#666' }}>
        {value}
      </span>
    </div>
  )
}

function truncateModel(model: string): string {
  if (model.length <= 20) return model
  return `${model.slice(0, 17)}...`
}

const styles: Record<string, React.CSSProperties> = {
  container: {
    backgroundColor: '#f9fafb',
    border: '1px solid #e5e7eb',
    borderRadius: '8px',
    padding: '16px',
    fontSize: '14px',
    lineHeight: '1.5',
  },
  header: {
    fontSize: '16px',
    fontWeight: '600',
    color: '#111',
    marginBottom: '12px',
    borderBottom: '2px solid #e5e7eb',
    paddingBottom: '8px',
  },
  workersList: {
    display: 'flex',
    flexDirection: 'column',
    gap: '12px',
  },
  workerCard: {
    backgroundColor: '#fff',
    border: '1px solid #d1d5db',
    borderRadius: '6px',
    padding: '12px',
  },
  workerHeader: {
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
    marginBottom: '8px',
  },
  healthDot: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
  },
  dot: {
    width: '12px',
    height: '12px',
    borderRadius: '50%',
    flexShrink: 0,
  },
  workerTitle: {
    fontWeight: '600',
    color: '#1f2937',
  },
  state: {
    fontSize: '12px',
    marginLeft: '8px',
    color: '#6b7280',
    fontWeight: 'normal',
  },
  workerDetails: {
    display: 'grid',
    gridTemplateColumns: '1fr 1fr',
    gap: '8px 12px',
    fontSize: '13px',
  },
  detailRow: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  detailLabel: {
    color: '#6b7280',
    fontWeight: '500',
  },
  detailValue: {
    color: '#1f2937',
    fontWeight: '500',
  },
  loading: {
    textAlign: 'center',
    color: '#6b7280',
    padding: '16px',
  },
  error: {
    textAlign: 'center',
    color: '#ef4444',
    padding: '16px',
  },
}
