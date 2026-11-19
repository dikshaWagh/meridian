import { useEffect, useRef, useState } from 'react'
import type { Catalog, CatalogModel, OCRResult } from '../types'
import { errorMessage, runOCR } from '../api'
import { Copy, ScanText } from './Icons'

interface MLStudioProps {
  catalog: Catalog | null
  onToast: (message: string, tone?: 'info' | 'error' | 'success') => void
}

function ModelChip({ model, fallback }: { model?: CatalogModel; fallback: string }) {
  if (!model) {
    return <span className="model-chip off">{fallback} · not configured</span>
  }
  const tone = model.state === 'ready' ? 'live' : model.state === 'starting' ? 'warn' : 'off'
  return (
    <span className="model-chip">
      <span className={`dot ${tone}`} />
      {model.id}
      <span className="model-chip-state">{model.state}</span>
    </span>
  )
}

export function MLStudio({ catalog, onToast }: MLStudioProps) {
  const ocrModel = catalog?.ml.find((model) => model.kind === 'ocr')

  const [dragActive, setDragActive] = useState(false)
  const [ocrBusy, setOcrBusy] = useState(false)
  const [ocrResult, setOcrResult] = useState<OCRResult | null>(null)
  const [ocrFileName, setOcrFileName] = useState('')
  const [preview, setPreview] = useState<string | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    return () => {
      if (preview) URL.revokeObjectURL(preview)
    }
  }, [preview])

  const pickImage = async (file: File) => {
    if (file.type !== 'image/png' && file.type !== 'image/jpeg') {
      onToast('Use a PNG or JPEG image.', 'error')
      return
    }
    if (file.size > 10 * 1024 * 1024) {
      onToast('That image is larger than the 10 MB upload limit.', 'error')
      return
    }
    setOcrBusy(true)
    setOcrResult(null)
    setOcrFileName(file.name)
    setPreview((current) => {
      if (current) URL.revokeObjectURL(current)
      return URL.createObjectURL(file)
    })
    try {
      const result = await runOCR(file)
      setOcrResult(result)
      onToast('Text extracted.', 'success')
    } catch (err) {
      onToast(errorMessage(err), 'error')
    } finally {
      setOcrBusy(false)
    }
  }

  return (
    <section className="ml-studio">
      <div className="ml-heading">
        <div>
          <h1>Document Analyzer</h1>
          <p>Extract text from a scanned page or screenshot. Everything runs on this machine.</p>
        </div>
        <div className="ml-badges">
          <ModelChip model={ocrModel} fallback="OCR" />
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div className="card-icon">
            <ScanText size={19} />
          </div>
          <div>
            <h2>Read a document</h2>
            <p>Upload a PNG or JPEG, then copy the extracted text.</p>
          </div>
          <span className="card-tag">OCR</span>
        </div>

        <input
          ref={fileInput}
          type="file"
          accept="image/png,image/jpeg"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0]
            if (file) void pickImage(file)
            event.target.value = ''
          }}
        />

        <button
          className={`dropzone ${dragActive ? 'dragging' : ''} ${ocrBusy ? 'busy' : ''}`}
          disabled={ocrBusy || !ocrModel}
          onClick={() => fileInput.current?.click()}
          onDragOver={(event) => {
            event.preventDefault()
            setDragActive(true)
          }}
          onDragLeave={() => setDragActive(false)}
          onDrop={(event) => {
            event.preventDefault()
            setDragActive(false)
            const file = event.dataTransfer.files?.[0]
            if (file) void pickImage(file)
          }}
        >
          {ocrBusy ? (
            <>
              <span className="spinner" />
              <span className="dropzone-title">Reading document…</span>
              <span className="dropzone-hint">Running on the OCR worker</span>
            </>
          ) : (
            <>
              <span className="dropzone-icon">
                <ScanText size={22} />
              </span>
              <span className="dropzone-title">Drop an image, or browse</span>
              <span className="dropzone-hint">PNG or JPEG · up to 10 MB</span>
            </>
          )}
        </button>

        {ocrResult && (
          <div className="ocr-result">
            <div className="ocr-preview">
              {preview ? <img src={preview} alt={ocrFileName} /> : null}
              <div className="ocr-file">
                <span className="ocr-file-name">{ocrFileName}</span>
                <span className="ocr-file-meta">
                  {ocrResult.lines.length} lines · {ocrResult.processing_ms} ms
                </span>
              </div>
            </div>
            <div className="ocr-text-wrap">
              <pre className="ocr-text">{ocrResult.text || '(no text detected)'}</pre>
              <button
                className="ghost-button"
                onClick={() => {
                  void navigator.clipboard?.writeText(ocrResult.text)
                  onToast('Copied extracted text.', 'success')
                }}
              >
                <Copy size={15} />
                Copy
              </button>
            </div>
          </div>
        )}
      </div>
    </section>
  )
}
