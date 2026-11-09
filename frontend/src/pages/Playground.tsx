import { useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { CodeBlock } from '../components/ui/CodeBlock'
import { Icon } from '../components/ui/Icon'
import { Panel } from '../components/ui/Panel'
import { Spinner } from '../components/ui/Spinner'
import { EmptyState } from '../components/ui/States'
import { PageHeader } from '../components/layout/PageHeader'
import { ModeBadge } from '../components/layout/ModeBadge'
import { useMeridianStatus } from '../hooks/useMeridianStatus'
import { useSimulation } from '../hooks/useSimulation'
import { ApiError, NetworkError, sendChat } from '../lib/api'
import type { ChatResult } from '../lib/api'
import { useMode } from '../lib/useMode'
import { formatDuration, formatNumber } from '../lib/formatters'
import { estimateRequestTokens } from '../simulation/estimate'
import type { TokenEstimate } from '../simulation/estimate'
import type { ChatMessage, ChatRequestBody, ChatResponse } from '../lib/types'

interface RequestError {
  status: number | null
  type: string
  message: string
}

interface SimulatedResult {
  data: ChatResponse
  worker: string
  estimate: TokenEstimate
  healthyCandidates: number
  roundRobinIndex: number
  elapsedMs: number
}

function errorCopy(error: RequestError): { title: string; hint: string } {
  switch (error.status) {
    case 400:
      return {
        title: 'Bad request (400)',
        hint: 'The balancer rejected the body. A model is required and the body must be valid JSON under 1 MiB.',
      }
    case 404:
      return {
        title: 'Unknown model (404)',
        hint: 'No worker serves this model. GET /v1/models lists the models that currently have a healthy worker.',
      }
    case 503:
      return {
        title: 'No healthy worker (503)',
        hint: 'All workers for this model are starting or unhealthy. The dashboard shows the current state machine.',
      }
    case 502:
      return {
        title: 'Upstream error (502)',
        hint: 'A worker was selected, but the upstream request failed. The upstream error message is shown above.',
      }
    case null:
      return {
        title: 'Backend unreachable',
        hint: 'Start Meridian or check the Vite proxy target in VITE_MERIDIAN_URL.',
      }
    default:
      return {
        title: `Request failed (${error.status})`,
        hint: 'The balancer returned an error response.',
      }
  }
}

function MetaItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-none border border-line bg-panel-alt px-3 py-2.5">
      <div className="mono-label">{label}</div>
      <div className="mt-1 truncate font-mono text-xs text-ink" title={value}>
        {value}
      </div>
    </div>
  )
}

export default function Playground() {
  const { mode } = useMode()
  const isLive = mode === 'live'
  const live = useMeridianStatus(isLive)
  const sim = useSimulation(!isLive)
  const models = isLive ? live.models : sim.models

  const [model, setModel] = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [prompt, setPrompt] = useState('')
  const [maxTokens, setMaxTokens] = useState('')
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<ChatResult | null>(null)
  const [simulated, setSimulated] = useState<SimulatedResult | null>(null)
  const [error, setError] = useState<RequestError | null>(null)
  const [elapsedMs, setElapsedMs] = useState<number | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    if (!model && models.length > 0) setModel(models[0] ?? '')
  }, [model, models])

  useEffect(() => {
    return () => abortRef.current?.abort()
  }, [])

  const messages = useMemo<ChatMessage[]>(() => {
    const list: ChatMessage[] = []
    if (systemPrompt.trim()) list.push({ role: 'system', content: systemPrompt.trim() })
    if (prompt.trim()) list.push({ role: 'user', content: prompt.trim() })
    return list
  }, [systemPrompt, prompt])

  const parsedMaxTokens = Number.parseInt(maxTokens, 10)

  const requestBody = useMemo<ChatRequestBody>(() => {
    const body: ChatRequestBody = { model: model.trim(), messages }
    if (Number.isFinite(parsedMaxTokens) && parsedMaxTokens > 0) body.max_tokens = parsedMaxTokens
    return body
  }, [model, messages, parsedMaxTokens])

  const canSend = model.trim().length > 0 && prompt.trim().length > 0 && !sending

  const resetResult = () => {
    setResult(null)
    setSimulated(null)
    setError(null)
    setElapsedMs(null)
  }

  const handleSend = async () => {
    if (!canSend) return
    setSending(true)
    resetResult()
    const started = performance.now()
    const body = requestBody

    if (isLive) {
      const controller = new AbortController()
      abortRef.current = controller
      try {
        const response = await sendChat(body, controller.signal)
        setResult(response)
        setElapsedMs(performance.now() - started)
      } catch (cause) {
        if (cause instanceof DOMException && cause.name === 'AbortError') {
          setSending(false)
          return
        }
        if (cause instanceof ApiError) {
          setError({ status: cause.status, type: cause.type, message: cause.message })
        } else if (cause instanceof NetworkError) {
          setError({ status: null, type: cause.name, message: cause.message })
        } else {
          setError({
            status: null,
            type: 'unknown',
            message: cause instanceof Error ? cause.message : 'Unexpected error.',
          })
        }
        setElapsedMs(performance.now() - started)
      } finally {
        setSending(false)
        abortRef.current = null
      }
      return
    }

    const estimate = estimateRequestTokens(messages, body.max_tokens)
    const routed = sim.sendRequest(body.model, estimate.total)
    if (!routed.ok) {
      setError({
        status: routed.status,
        type: routed.status === 404 ? 'model_not_found' : 'no_healthy_worker',
        message: routed.message,
      })
      setElapsedMs(performance.now() - started)
      setSending(false)
      return
    }

    const reply: ChatResponse = {
      id: `sim-${routed.workerId}-${sim.requestCounter()}`,
      object: 'chat.completion',
      model: routed.model,
      choices: [
        {
          index: 0,
          message: {
            role: 'assistant',
            content: `[Simulated · ${routed.workerId} · ${routed.model}] reply to: ${prompt
              .trim()
              .slice(0, 160)}`,
          },
          finish_reason: 'stop',
        },
      ],
      usage: {
        prompt_tokens: estimate.input,
        completion_tokens: estimate.output,
        total_tokens: estimate.total,
      },
    }
    setSimulated({
      data: reply,
      worker: routed.workerId,
      estimate,
      healthyCandidates: routed.healthyCandidates,
      roundRobinIndex: routed.roundRobinIndex,
      elapsedMs: performance.now() - started,
    })
    setElapsedMs(performance.now() - started)
    setSending(false)
  }

  const onPromptKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
      event.preventDefault()
      void handleSend()
    }
  }

  const responseContent = result?.data.choices[0]?.message.content ?? simulated?.data.choices[0]?.message.content
  const usage = result?.data.usage ?? simulated?.data.usage
  const usageAvailable = Boolean(
    usage && (usage.total_tokens > 0 || usage.prompt_tokens > 0 || usage.completion_tokens > 0),
  )
  const copy = error ? errorCopy(error) : null

  return (
    <div>
      <PageHeader
        eyebrow="LLM balancer · request console"
        title="Playground"
        description={
          isLive
            ? 'Send a real POST /v1/chat/completions through the Vite proxy. Round-trip time is measured in this browser; token estimates come from response headers.'
            : 'Simulated request path: model selection, per-model round robin, health filtering, 404/503 errors and response shaping all run locally.'
        }
        badges={
          <>
            <ModeBadge />
            <Badge tone="neutral">POST /v1/chat/completions</Badge>
            <Badge tone="neutral">non-streaming</Badge>
          </>
        }
      />

      {!isLive ? (
        <div className="mb-5 flex flex-col gap-3 rounded-none border border-ml border-dashed bg-mock-soft px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <Icon name="sparkles" className="mt-0.5 h-4 w-4 shrink-0 text-ml" />
            <div>
              <p className="text-xs font-medium text-ink">Simulated responses — no backend call</p>
              <p className="mt-1 text-[11px] leading-relaxed text-dim">
                Requests run through the local balancer mirror. Errors match the real 404 and 503
                messages, but no Go process is involved.
              </p>
            </div>
          </div>
          <Badge tone="warn">Simulated</Badge>
        </div>
      ) : live.offline ? (
        <div className="mb-5 flex items-center gap-2 rounded-none border border-bad bg-bad-soft px-4 py-3 text-[11px] text-dim">
          <Icon name="alert" className="h-4 w-4 shrink-0 text-bad" />
          The backend is unreachable. Requests will fail with a network error until it is running.
        </div>
      ) : null}

      <div className="grid gap-4 xl:grid-cols-[1.02fr_0.98fr]">
        <div className="space-y-4">
          <Panel
            title="Request"
            subtitle="Only model, messages and max_tokens are sent. No unsupported fields are added."
            actions={
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  resetResult()
                  setPrompt('')
                  setSystemPrompt('')
                  setMaxTokens('')
                }}
              >
                <Icon name="trash" className="h-3.5 w-3.5" />
                Clear
              </Button>
            }
          >
            <div className="space-y-4">
              <div>
                <label htmlFor="model" className="mono-label mb-1.5 block">
                  Model
                </label>
                <input
                  id="model"
                  className="input font-mono"
                  value={model}
                  onChange={(event) => setModel(event.target.value)}
                  placeholder="qwen2.5-3b"
                  list="model-options"
                  autoComplete="off"
                  spellCheck={false}
                />
                <datalist id="model-options">
                  {models.map((item) => (
                    <option key={item} value={item} />
                  ))}
                </datalist>
                {models.length > 0 ? (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {models.map((item) => (
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
                ) : (
                  <p className="mt-2 text-[11px] text-dim">
                    No models advertised{isLive ? ' — the backend may be offline.' : '.'} You can
                    still type a model to test a 404.
                  </p>
                )}
              </div>

              <div>
                <label htmlFor="system" className="mono-label mb-1.5 block">
                  System message <span className="normal-case tracking-normal">(optional)</span>
                </label>
                <textarea
                  id="system"
                  className="textarea"
                  rows={2}
                  value={systemPrompt}
                  onChange={(event) => setSystemPrompt(event.target.value)}
                  placeholder="You are a concise assistant."
                />
              </div>

              <div>
                <label htmlFor="prompt" className="mono-label mb-1.5 block">
                  User message
                </label>
                <textarea
                  id="prompt"
                  className="textarea"
                  rows={6}
                  value={prompt}
                  onChange={(event) => setPrompt(event.target.value)}
                  onKeyDown={onPromptKeyDown}
                  placeholder="Explain what a per-model round robin does in two sentences."
                />
                <p className="mt-1.5 text-[10px] text-dim">
                  Ctrl/⌘ + Enter sends. A response is non-streaming.
                </p>
              </div>

              <div className="max-w-[200px]">
                <label htmlFor="max-tokens" className="mono-label mb-1.5 block">
                  max_tokens <span className="normal-case tracking-normal">(optional)</span>
                </label>
                <input
                  id="max-tokens"
                  className="input font-mono"
                  type="number"
                  min={1}
                  value={maxTokens}
                  onChange={(event) => setMaxTokens(event.target.value)}
                  placeholder="128"
                />
              </div>

              <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
                <Button variant="primary" onClick={() => void handleSend()} disabled={!canSend}>
                  {sending ? <Spinner className="h-4 w-4" /> : <Icon name="play" className="h-4 w-4" />}
                  {sending ? 'Sending…' : 'Send request'}
                </Button>
                {!prompt.trim() ? (
                  <span className="text-[11px] text-dim">A user message is required.</span>
                ) : null}
              </div>
            </div>
          </Panel>

          <Panel title="Request body" subtitle="Exactly what is sent to the balancer.">
            <CodeBlock code={JSON.stringify(requestBody, null, 2)} label="json" maxHeight="220px" />
          </Panel>
        </div>

        <div className="space-y-4">
          <Panel
            title="Response"
            subtitle={
              isLive
                ? 'Worker and token estimates come from X-Meridian-* response headers.'
                : 'Worker, token estimates and usage are simulated locally.'
            }
            actions={
              !isLive ? (
                <Badge tone="warn">Simulated</Badge>
              ) : elapsedMs !== null ? (
                <Badge tone="neutral">{formatDuration(elapsedMs)}</Badge>
              ) : undefined
            }
          >
            {sending ? (
              <div
                role="status"
                aria-live="polite"
                className="flex flex-col items-center justify-center py-14 text-center"
              >
                <Spinner className="h-5 w-5 text-accent" />
                <p className="mt-3 text-xs text-dim">Waiting for the response…</p>
              </div>
            ) : error && copy ? (
              <div
                role="alert"
                className="rounded-none border border-bad bg-bad-soft p-4"
              >
                <div className="flex items-start gap-3">
                  <Icon name="alert" className="mt-0.5 h-4 w-4 shrink-0 text-bad" />
                  <div>
                    <p className="text-sm font-medium text-ink">{copy.title}</p>
                    <p className="mt-1 font-mono text-[11px] leading-relaxed text-bad">
                      {error.message}
                    </p>
                    <p className="mt-2 text-[11px] leading-relaxed text-dim">{copy.hint}</p>
                    {error.type ? (
                      <p className="mt-2 font-mono text-[10px] uppercase tracking-wider text-dim">
                        type: {error.type}
                      </p>
                    ) : null}
                  </div>
                </div>
              </div>
            ) : result ? (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                  <MetaItem label="Worker" value={result.meta.worker ?? '—'} />
                  <MetaItem label="Model" value={result.meta.model ?? result.data.model} />
                  <MetaItem label="HTTP" value={`${result.status} OK`} />
                  <MetaItem
                    label="Est. input"
                    value={result.meta.estInput !== null ? formatNumber(result.meta.estInput) : '—'}
                  />
                  <MetaItem
                    label="Est. output"
                    value={result.meta.estOutput !== null ? formatNumber(result.meta.estOutput) : '—'}
                  />
                  <MetaItem
                    label="Est. total"
                    value={result.meta.estTotal !== null ? formatNumber(result.meta.estTotal) : '—'}
                  />
                </div>

                <div>
                  <p className="mono-label mb-2">Assistant message</p>
                  <div className="max-h-56 overflow-auto rounded-none border border-line bg-panel-alt p-3 text-[13px] leading-relaxed text-ink">
                    {responseContent || 'The response contained no message content.'}
                  </div>
                </div>

                <div>
                  <p className="mono-label mb-2">Usage in response body</p>
                  {usageAvailable ? (
                    <p className="font-mono text-xs text-dim">
                      prompt {formatNumber(usage?.prompt_tokens ?? 0)} · completion{' '}
                      {formatNumber(usage?.completion_tokens ?? 0)} · total{' '}
                      {formatNumber(usage?.total_tokens ?? 0)}
                    </p>
                  ) : (
                    <p className="text-[11px] text-dim">
                      The response body did not include non-zero usage values.
                    </p>
                  )}
                </div>

                <p className="text-[11px] leading-relaxed text-dim">
                  Estimates come from the X-Meridian-Est-* headers. Actual usage is logged
                  server-side and is only shown here if the worker returned a usage block in its own
                  response body.
                </p>

                <CodeBlock
                  code={JSON.stringify(result.data, null, 2)}
                  label="response json"
                  maxHeight="300px"
                />
              </div>
            ) : simulated ? (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                  <MetaItem label="Worker" value={simulated.worker} />
                  <MetaItem label="Model" value={simulated.data.model} />
                  <MetaItem label="HTTP" value="200 Simulated" />
                  <MetaItem label="Est. input" value={formatNumber(simulated.estimate.input)} />
                  <MetaItem label="Est. output" value={formatNumber(simulated.estimate.output)} />
                  <MetaItem label="Est. total" value={formatNumber(simulated.estimate.total)} />
                </div>

                <div className="rounded-none border border-ml border-dashed bg-mock-soft px-3 py-2 text-[11px] leading-relaxed text-dim">
                  Round robin chose <span className="font-mono text-ink">{simulated.worker}</span>{' '}
                  (index {simulated.roundRobinIndex + 1} of {simulated.healthyCandidates} healthy
                  candidates). No backend call was made.
                </div>

                <div>
                  <p className="mono-label mb-2">Assistant message</p>
                  <div className="max-h-56 overflow-auto rounded-none border border-line bg-panel-alt p-3 text-[13px] leading-relaxed text-ink">
                    {responseContent}
                  </div>
                </div>

                <div>
                  <p className="mono-label mb-2">Simulated usage</p>
                  <p className="font-mono text-xs text-dim">
                    prompt {formatNumber(usage?.prompt_tokens ?? 0)} · completion{' '}
                    {formatNumber(usage?.completion_tokens ?? 0)} · total{' '}
                    {formatNumber(usage?.total_tokens ?? 0)}
                  </p>
                </div>

                <CodeBlock
                  code={JSON.stringify(simulated.data, null, 2)}
                  label="simulated response json"
                  maxHeight="300px"
                />
              </div>
            ) : (
              <EmptyState
                icon="playground"
                title="No request sent yet"
                message="Compose a message and send it. The response panel will show the selected worker, token estimates, elapsed time and any error returned by the balancer."
              />
            )}
          </Panel>

          {isLive ? (
            <div className="rounded-none border border-line bg-panel px-4 py-3">
              <p className="text-[11px] leading-relaxed text-dim">
                The balancer streams responses as-is, but this playground sends non-streaming
                requests so it can read worker headers and a complete response body. Unknown models
                return 404; models with no healthy worker return 503.
              </p>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
