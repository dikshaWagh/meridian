import type { AdminCluster, AdminNode, AdminReplica } from '../types'

// The graph draws the leader at the top and every other node in a row below
// it, with each node's instances listed inside its box. A routed request lights
// the leader → node edge, the node and the replica it used. The lit set comes
// from route events and expires after a short time.

const WIDTH = 1000
const HEADER = 64
const ROW = 30
const PAD = 14
const LEADER_W = 360
const NODE_MAX_W = 300

interface Props {
  cluster: AdminCluster
  lit: Record<string, number>
  now: number
}

interface Box {
  node: AdminNode
  x: number
  y: number
  w: number
  h: number
}

function boxHeight(n: AdminNode): number {
  return HEADER + Math.max(1, n.replicas.length) * ROW + PAD
}

// litKeys lists what a route event lights up. The leader has no edge to
// itself, so its edge key is never drawn.
export function litKeys(nodeID: string, replicaID: string): string[] {
  const keys = [`node:${nodeID}`, `edge:${nodeID}`]
  if (replicaID) keys.push(`rep:${replicaID}`)
  return keys
}

export function ClusterGraph({ cluster, lit, now }: Props) {
  const leader = cluster.nodes.find((n) => n.local) ?? null
  const others = cluster.nodes.filter((n) => !n.local)

  const leaderH = leader ? boxHeight(leader) : 0
  const leaderBox: Box | null = leader
    ? { node: leader, x: (WIDTH - LEADER_W) / 2, y: 24, w: LEADER_W, h: leaderH }
    : null

  const rowY = (leaderBox ? leaderBox.y + leaderH : 0) + 120
  const slot = others.length > 0 ? WIDTH / others.length : WIDTH
  const boxW = Math.min(NODE_MAX_W, slot - 40)
  const boxes: Box[] = others.map((node, i) => ({
    node,
    x: i * slot + (slot - boxW) / 2,
    y: rowY,
    w: boxW,
    h: boxHeight(node),
  }))

  const bottom = Math.max(rowY + Math.max(0, ...boxes.map((b) => b.h)), leaderBox ? leaderBox.y + leaderH : 0) + 30
  const isLit = (key: string) => (lit[key] ?? 0) > now

  return (
    <svg className="graph" viewBox={`0 0 ${WIDTH} ${bottom}`} role="img" aria-label="Cluster routing graph">
      <defs>
        <linearGradient id="gHead" x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor="var(--accent)" stopOpacity="0.18" />
          <stop offset="1" stopColor="var(--accent)" stopOpacity="0" />
        </linearGradient>
      </defs>

      {leaderBox &&
        boxes.map((b) => {
          const x1 = leaderBox.x + leaderBox.w / 2
          const y1 = leaderBox.y + leaderBox.h
          const x2 = b.x + b.w / 2
          const y2 = b.y
          const mid = (y1 + y2) / 2
          const active = isLit(`edge:${b.node.node_id}`)
          return (
            <path
              key={`edge-${b.node.node_id}`}
              className={active ? 'edge lit' : 'edge'}
              d={`M ${x1} ${y1} C ${x1} ${mid}, ${x2} ${mid}, ${x2} ${y2}`}
            />
          )
        })}

      {leaderBox && <NodeBox box={leaderBox} lit={isLit} />}
      {boxes.map((b) => (
        <NodeBox key={b.node.node_id} box={b} lit={isLit} />
      ))}

      {cluster.nodes.length === 0 && (
        <text x={WIDTH / 2} y={80} textAnchor="middle" className="graph-empty">
          No nodes registered yet.
        </text>
      )}
    </svg>
  )
}

function NodeBox({ box, lit }: { box: Box; lit: (key: string) => boolean }) {
  const { node, x, y, w, h } = box
  const active = lit(`node:${node.node_id}`)
  const online = node.state === 'online'
  return (
    <g className={active ? 'node lit' : 'node'}>
      <rect x={x} y={y} width={w} height={h} rx={16} className="node-body" />
      <rect x={x} y={y} width={w} height={HEADER} rx={16} className="node-head" />
      <circle cx={x + 22} cy={y + 24} r={6} className={online ? 'dot dot-ok' : 'dot dot-bad'} />
      <text x={x + 36} y={y + 28} className="node-title">
        {node.local ? 'Leader' : 'Worker'} · {node.address}
      </text>
      <text x={x + 36} y={y + 48} className="node-sub">
        {node.node_id.slice(0, 12)} · {node.cpu_cores} cores
        {node.ram_total_mb ? ` · RAM ${node.ram_used_mb}/${node.ram_total_mb} MB` : ''}
      </text>

      {node.replicas.length === 0 && (
        <text x={x + 16} y={y + HEADER + 20} className="rep-sub">
          no instances
        </text>
      )}
      {node.replicas.map((r, i) => (
        <ReplicaRow
          key={r.id}
          rep={r}
          x={x + 10}
          y={y + HEADER + i * ROW + 6}
          w={w - 20}
          lit={lit(`rep:${r.id}`)}
        />
      ))}
    </g>
  )
}

function ReplicaRow({ rep, x, y, w, lit }: { rep: AdminReplica; x: number; y: number; w: number; lit: boolean }) {
  const cls = `rep rep-${rep.healthy ? 'ready' : rep.state}${lit ? ' lit' : ''}`
  return (
    <g className={cls}>
      <rect x={x} y={y} width={w} height={ROW - 6} rx={8} />
      <text x={x + 10} y={y + 15} className="rep-name">
        {rep.model} #{rep.instance}
      </text>
      <text x={x + w - 10} y={y + 15} textAnchor="end" className="rep-pid">
        {rep.pid > 0 ? `PID ${rep.pid}` : 'PID —'} · {rep.state}
      </text>
    </g>
  )
}
