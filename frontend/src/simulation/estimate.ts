import type { ChatMessage } from '../lib/types'

// Mirrors internal/tokens/estimate.go for simulated requests only.
const CHARS_PER_TOKEN = 4
const PER_MESSAGE_OVERHEAD = 4
const REPLY_PRIMING = 3
const DEFAULT_OUTPUT_TOKENS = 128

function textTokens(text: string): number {
  const length = [...text].length
  if (length === 0) return 0
  return Math.ceil(length / CHARS_PER_TOKEN)
}

export interface TokenEstimate {
  input: number
  output: number
  total: number
}

export function estimateRequestTokens(messages: ChatMessage[], maxTokens?: number): TokenEstimate {
  let input = REPLY_PRIMING
  for (const message of messages) input += textTokens(message.content) + PER_MESSAGE_OVERHEAD
  const output = maxTokens && maxTokens > 0 ? maxTokens : DEFAULT_OUTPUT_TOKENS
  return { input, output, total: input + output }
}
