import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

export interface ScoreDatum {
  id: string
  latency: number
  queue: number
  gpu: number
  cpu: number
}

export function ScoreBreakdown({ data }: { data: ScoreDatum[] }) {
  return (
    <div style={{ width: '100%', height: 210 }}>
      <ResponsiveContainer>
        <BarChart data={data} layout="vertical" margin={{ top: 4, right: 12, left: 4, bottom: 0 }}>
          <CartesianGrid stroke="rgba(0,0,0,0.09)" horizontal={false} />
          <XAxis
            type="number"
            domain={[0, 1]}
            tick={{ fill: '#6B6B6B', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            axisLine={{ stroke: 'rgba(0,0,0,0.18)' }}
            tickLine={false}
          />
          <YAxis
            type="category"
            dataKey="id"
            width={72}
            tick={{ fill: '#222222', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            axisLine={false}
            tickLine={false}
          />
          <Tooltip
            contentStyle={{
              background: '#F8F8F8',
              border: '1px solid #333333',
              borderRadius: 0,
              fontSize: 12,
              color: '#222222',
              boxShadow: 'none',
            }}
            labelStyle={{ color: '#6B6B6B', fontFamily: 'var(--font-mono)', fontSize: 10 }}
            formatter={(value) => Number(value).toFixed(3)}
          />
          <Legend wrapperStyle={{ fontSize: 10, fontFamily: 'var(--font-mono)' }} />
          <Bar
            dataKey="latency"
            name="latency · 0.40"
            stackId="score"
            fill="#E8581C"
            barSize={10}
            isAnimationActive={false}
          />
          <Bar
            dataKey="queue"
            name="queue · 0.25"
            stackId="score"
            fill="#C2410C"
            barSize={10}
            isAnimationActive={false}
          />
          <Bar
            dataKey="gpu"
            name="gpu · 0.20"
            stackId="score"
            fill="#3C5A7A"
            barSize={10}
            isAnimationActive={false}
          />
          <Bar
            dataKey="cpu"
            name="cpu · 0.15"
            stackId="score"
            fill="#8A8A8A"
            barSize={10}
            isAnimationActive={false}
          />
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
