/**
 * ML mock routing weights. Constants live here so the scoring model is easy to
 * replace later with values from a real ML status endpoint.
 */
export const ML_WEIGHTS = {
  latency: 0.4,
  queue: 0.25,
  gpu: 0.2,
  cpu: 0.15,
} as const

export type MLWeights = typeof ML_WEIGHTS
