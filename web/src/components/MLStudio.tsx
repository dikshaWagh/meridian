import { useEffect, useRef, useState } from 'react'
import type { Catalog, CatalogModel, ImageResult, OCRResult, SystemState } from '../types'
import { generateImage, errorMessage, runOCR } from '../api'
import { Bolt, Check, Copy, Download, ImageIcon, ScanText, Sparkle, Trash } from './Icons'

interface MLStudioProps {
  state: SystemState | null
  catalog: Catalog | null
  onToast: (message: string, tone?: 'info' | 'error' | 'success') => void
}

const GALLERY_KEY = 'meridian.gallery.v1'

function loadGallery(): ImageResult[] {
  try {
    const raw = localStorage.getItem(GALLERY_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as ImageResult[]).slice(0, 24) : []
  } catch {
    return []
  }
}

function randomSeed(): number {
  return Math.floor(Math.random() * 2 ** 31)
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

export function MLStudio({ state, catalog, onToast }: MLStudioProps) {
  const active = state?.phase === 'active' && state.mode === 'ml'
  const ocrModel = catalog?.ml.find((model) => model.kind === 'ocr')
  const imageModel = catalog?.ml.find((model) => model.kind === 'image-generation')

  const [dragActive, setDragActive] = useState(false)
  const [ocrBusy, setOcrBusy] = useState(false)
  const [ocrResult, setOcrResult] = useState<OCRResult | null>(null)
  const [ocrFileName, setOcrFileName] = useState('')
  const [preview, setPreview] = useState<string | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const [prompt, setPrompt] = useState('')
  const [negativePrompt, setNegativePrompt] = useState('')
  const [width, setWidth] = useState(512)
  const [height, setHeight] = useState(512)
  const [steps, setSteps] = useState(25)
  const [seed, setSeed] = useState(randomSeed)
  const [genBusy, setGenBusy] = useState(false)
  const [gallery, setGallery] = useState<ImageResult[]>(loadGallery)

  useEffect(() => {
    try {
      localStorage.setItem(GALLERY_KEY, JSON.stringify(gallery))
    } catch {
      // Storage is optional; the session gallery still works.
    }
  }, [gallery])

  useEffect(() => {
    return () => {
      if (preview) URL.revokeObjectURL(preview)
    }
  }, [preview])

  const pickImage = async (file: File) => {
    if (!active) {
      onToast('Switch to ML Studio before running OCR.', 'info')
      return
    }
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

  const generate = async () => {
    if (!active) {
      onToast('Switch to ML Studio before generating an image.', 'info')
      return
    }
    if (!prompt.trim()) {
      onToast('Describe the image first.', 'info')
      return
    }
    setGenBusy(true)
    try {
      const result = await generateImage({
        prompt: prompt.trim(),
        negative_prompt: negativePrompt.trim() || undefined,
        width,
        height,
        steps,
        seed,
      })
      setGallery((current) => [{ ...result, prompt: prompt.trim() }, ...current].slice(0, 24))
      onToast('Image generated.', 'success')
    } catch (err) {
      onToast(errorMessage(err), 'error')
    } finally {
      setGenBusy(false)
    }
  }

  return (
    <section className="ml-studio">
      <div className="ml-heading">
        <div>
          <h1>ML Studio</h1>
          <p>On-device OCR and image generation. The language models stay unloaded while this mode is active.</p>
        </div>
        <div className="ml-badges">
          <ModelChip model={ocrModel} fallback="OCR" />
          <ModelChip model={imageModel} fallback="Image" />
        </div>
      </div>

      <div className="ml-grid">
        <div className="card">
          <div className="card-head">
            <div className="card-icon">
              <ScanText size={19} />
            </div>
            <div>
              <h2>Read an image</h2>
              <p>Extract text from a PNG or JPEG.</p>
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
            disabled={!active || ocrBusy || !ocrModel}
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
                <span className="dropzone-title">Reading image…</span>
                <span className="dropzone-hint">Running on the ML worker</span>
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

        <div className="card">
          <div className="card-head">
            <div className="card-icon">
              <ImageIcon size={19} />
            </div>
            <div>
              <h2>Imagine</h2>
              <p>Text-to-image with a local diffusion model.</p>
            </div>
            <span className="card-tag">Diffusion</span>
          </div>

          <label className="field">
            <span className="field-label">Prompt</span>
            <textarea
              rows={3}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="A quiet mountain lake at sunrise, soft mist, cinematic light"
            />
          </label>

          <details className="advanced">
            <summary>Advanced</summary>
            <label className="field">
              <span className="field-label">Negative prompt</span>
              <input
                value={negativePrompt}
                onChange={(event) => setNegativePrompt(event.target.value)}
                placeholder="blurry, low contrast, extra fingers"
              />
            </label>
            <div className="field-row">
              <label className="field">
                <span className="field-label">Size</span>
                <select
                  value={`${width}x${height}`}
                  onChange={(event) => {
                    const [w, h] = event.target.value.split('x').map(Number)
                    setWidth(w)
                    setHeight(h)
                  }}
                >
                  <option value="384x384">384 × 384</option>
                  <option value="512x512">512 × 512</option>
                  <option value="640x640">640 × 640</option>
                  <option value="768x768">768 × 768</option>
                </select>
              </label>
              <label className="field">
                <span className="field-label">Seed</span>
                <div className="seed-row">
                  <input
                    type="number"
                    value={seed}
                    onChange={(event) => setSeed(Number(event.target.value) || 0)}
                  />
                  <button className="icon-button bordered" onClick={() => setSeed(randomSeed())} aria-label="Random seed">
                    <Sparkle size={15} />
                  </button>
                </div>
              </label>
            </div>
          </details>

          <label className="field">
            <span className="field-label">
              Steps <span className="field-value">{steps}</span>
            </span>
            <input
              className="range"
              type="range"
              min={5}
              max={40}
              value={steps}
              onChange={(event) => setSteps(Number(event.target.value))}
            />
          </label>

          <div className="generate-row">
            <button className="primary-button grow" onClick={() => void generate()} disabled={!active || genBusy || !imageModel}>
              {genBusy ? (
                <>
                  <span className="spinner small" />
                  Generating…
                </>
              ) : (
                <>
                  <ImageIcon size={17} />
                  Generate image
                </>
              )}
            </button>
            <span className="generate-seed">seed {seed}</span>
          </div>
          <p className="card-note">
            <Bolt size={13} />
            CPU diffusion can take a few minutes. The request stays open while the worker renders.
          </p>
        </div>
      </div>

      <div className="card gallery-card">
        <div className="card-head">
          <div className="card-icon">
            <Sparkle size={19} />
          </div>
          <div>
            <h2>Recent renders</h2>
            <p>Artifacts are stored by the server and served by opaque id.</p>
          </div>
          {gallery.length > 0 && (
            <button className="ghost-button" onClick={() => setGallery([])}>
              <Trash size={15} />
              Clear
            </button>
          )}
        </div>

        {gallery.length === 0 ? (
          <div className="gallery-empty">
            <ImageIcon size={26} />
            <span>Nothing generated yet. Your renders will appear here.</span>
          </div>
        ) : (
          <div className="gallery">
            {gallery.map((image) => (
              <figure className="image-card" key={image.id}>
                <div className="image-frame">
                  <img src={image.url} alt={image.prompt ?? 'Generated image'} loading="lazy" />
                  <a className="image-download" href={image.url} download={`meridian-${image.id}.png`} aria-label="Download image">
                    <Download size={16} />
                  </a>
                </div>
                <figcaption>
                  <span className="image-prompt">{image.prompt ?? 'Generated image'}</span>
                  <span className="image-meta">
                    {image.width}×{image.height} · {image.steps} steps · {image.processing_ms} ms
                  </span>
                </figcaption>
                <span className="image-check" aria-hidden="true">
                  <Check size={12} />
                </span>
              </figure>
            ))}
          </div>
        )}
      </div>
    </section>
  )
}
