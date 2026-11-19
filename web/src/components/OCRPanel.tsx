import { useEffect, useState } from 'react'
import { errorMessage, fetchMLStatus, runOCR } from '../api'
import { usePoll } from '../hooks/usePoll'
import type { OCRResponse } from '../types'

export function OCRPanel() {
  const { data: status, error: statusError } = usePoll(fetchMLStatus, 3000)
  const [file, setFile] = useState<File | null>(null)
  const [preview, setPreview] = useState<string | null>(null)
  const [model, setModel] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<OCRResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!file) return
    const url = URL.createObjectURL(file)
    setPreview(url)
    return () => URL.revokeObjectURL(url)
  }, [file])

  if (!status && statusError) {
    return (
      <section className="panel ocr">
        <h2>Document OCR</h2>
        <p className="error-line">Cannot reach the gateway: {statusError}</p>
      </section>
    )
  }
  if (!status) return <section className="panel ocr"><p className="muted">Loading…</p></section>

  if (!status.enabled) {
    return (
      <section className="panel ocr">
        <h2>Document OCR</h2>
        <div className="empty">
          <p>OCR is disabled on this server.</p>
          <p className="muted small">
            Start it with <code>--enable-ml --ml-config configs/ml.yaml</code>.
          </p>
        </div>
      </section>
    )
  }

  const selectedModel = model || status.models[0]?.id || ''

  const run = async () => {
    if (!file || running) return
    setRunning(true)
    setError(null)
    setResult(null)
    try {
      setResult(await runOCR(file, selectedModel))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRunning(false)
    }
  }

  const r = status.resources
  return (
    <section className="panel ocr">
      <div className="panel-head">
        <h2>Document OCR</h2>
        <span className="muted small">admission: {r.policy}</span>
      </div>

      <div className="summary">
        <div className="stat">
          <div className="stat-value">{r.ram_known ? `${r.ram_available_mb} MB` : '—'}</div>
          <div className="stat-label">RAM available</div>
        </div>
        <div className="stat">
          <div className="stat-value">{r.ram_reserved_mb} MB</div>
          <div className="stat-label">reserved for OCR</div>
        </div>
        <div className="stat">
          <div className="stat-value">{r.ram_headroom_mb} MB</div>
          <div className="stat-label">headroom</div>
        </div>
      </div>
      {r.probe_error && <div className="error-line">{r.probe_error}</div>}

      {status.workers.map((w) => (
        <div key={w.id} className={`worker worker-${w.state}`}>
          <div className="worker-head">
            <span className="dot" />
            <strong>{w.model_id}</strong>
            <span className={`badge badge-${w.state}`}>{w.state}</span>
          </div>
          <dl className="metrics">
            <div className="metric"><dt>engine</dt><dd>{w.engine} · {w.device}</dd></div>
            <div className="metric"><dt>served</dt><dd>{w.served}</dd></div>
            <div className="metric"><dt>failed</dt><dd>{w.failed}</dd></div>
            <div className="metric"><dt>restarts</dt><dd>{w.restarts}</dd></div>
            <div className="metric"><dt>last</dt><dd>{w.last_inference_ms} ms</dd></div>
          </dl>
          {w.last_error && <div className="error-line">{w.last_error}</div>}
        </div>
      ))}

      <div className="ocr-upload">
        <label className="control">
          <span>Image (PNG or JPEG)</span>
          <input
            type="file"
            accept="image/png,image/jpeg"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null)
              setResult(null)
              setError(null)
            }}
          />
        </label>
        {status.models.length > 1 && (
          <label className="control">
            <span>Model</span>
            <select value={selectedModel} onChange={(e) => setModel(e.target.value)}>
              {status.models.map((m) => (
                <option key={m.id} value={m.id}>{m.id} · {m.state}</option>
              ))}
            </select>
          </label>
        )}
        <button className="btn btn-primary" onClick={run} disabled={!file || running}>
          {running ? 'Reading…' : 'Run OCR'}
        </button>
      </div>

      {error && <div className="error-line">{error}</div>}

      {preview && (
        <div className="ocr-grid">
          <img className="ocr-preview" src={preview} alt="Selected document" />
          <div>
            {result ? (
              <>
                <div className="muted small">
                  {result.model} · {result.processing_ms} ms · {result.lines.length} lines
                </div>
                <pre className="ocr-text">{result.text || '(no text recognised)'}</pre>
                <button className="btn" onClick={() => void navigator.clipboard?.writeText(result.text)}>
                  Copy text
                </button>
                {result.lines.some((l) => l.confidence !== undefined) && (
                  <table className="ocr-lines">
                    <thead>
                      <tr><th>line</th><th>confidence</th></tr>
                    </thead>
                    <tbody>
                      {result.lines.map((l, i) => (
                        <tr key={i}>
                          <td>{l.text}</td>
                          <td>{l.confidence === undefined ? '—' : `${Math.round(l.confidence * 100)}%`}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </>
            ) : (
              <p className="muted small">Run OCR to see the recognised text.</p>
            )}
          </div>
        </div>
      )}
    </section>
  )
}
