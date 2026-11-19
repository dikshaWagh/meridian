# Web UI Fixes for Phase 1 — Real-Time Status Monitor

## Issues Found
1. No real-time worker status display (tok_per_sec, sessions, state)
2. Missing API calls for `/status` endpoint
3. No visual indicators for worker health
4. No session distribution display

## Fixes to Implement

### 1. Add Status Types (types.ts)
Add worker status types to display real-time metrics

### 2. Add API Status Fetch (api.ts)
Add function to fetch `/status` endpoint with worker metrics

### 3. Create StatusMonitor Component (components/StatusMonitor.tsx)
New component showing:
- Worker health (healthy/unhealthy with green/red indicator)
- tok_per_sec (actual token rate)
- Sessions count
- In-flight requests
- Served requests total

### 4. Integrate into App.tsx
Display StatusMonitor alongside ChatView

### 5. Auto-refresh every 2 seconds
Use setInterval to poll `/status` and update display

## What the UI Will Show

```
┌─────────────────────────────┐
│ WORKER STATUS               │
├─────────────────────────────┤
│ w1 ● healthy                │
│    Model: qwen2.5...        │
│    Tok/s: 42.3              │
│    Sessions: 5              │
│    In Flight: 1             │
│    Served: 42               │
│                             │
│ w2 ● healthy                │
│    Model: qwen2.5...        │
│    Tok/s: 39.8              │
│    Sessions: 5              │
│    In Flight: 0             │
│    Served: 38               │
└─────────────────────────────┘
```

After killing w2:
```
│ w2 ● UNHEALTHY ← RED!       │
│    Model: qwen2.5...        │
│    Tok/s: 0                 │
│    Sessions: 0              │
│    In Flight: 0             │
│    Served: 38               │
```

