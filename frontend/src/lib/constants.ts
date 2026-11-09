/** GET /status is polled at roughly this interval while the tab is visible. */
export const STATUS_POLL_MS = 2000

/** Mirrors internal/balancer: probes run every 2 s, 3 consecutive failures trip unhealthy. */
export const HEALTH_PROBE_MS = 2000
export const FAILURE_THRESHOLD = 3
export const STARTING_GRACE_MS = 180_000

export const MODE_STORAGE_KEY = 'meridian.mode'
