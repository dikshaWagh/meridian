import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { ObservedPoint } from '../../hooks/useObservedHistory'

interface ObservedChartProps {
  data: ObservedPoint[]
  height?: number
}

export function ObservedChart({ data, height = 190 }: ObservedChartProps) {
  return (
    <div style={{ width: '100%', height }}>
      <ResponsiveContainer>
        <LineChart data={data} margin={{ top: 8, right: 8, left: -18, bottom: 0 }}>
          <CartesianGrid stroke="rgba(0,0,0,0.09)" vertical={false} />
          <XAxis
            dataKey="t"
            tickFormatter={(value: number) =>
              new Date(value).toLocaleTimeString([], {
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit',
              })
            }
            tick={{ fill: '#6B6B6B', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            axisLine={{ stroke: 'rgba(0,0,0,0.18)' }}
            tickLine={false}
            minTickGap={28}
          />
          <YAxis
            allowDecimals={false}
            tick={{ fill: '#6B6B6B', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            axisLine={false}
            tickLine={false}
            width={44}
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
            labelFormatter={(value) => new Date(Number(value)).toLocaleTimeString()}
            formatter={(value: number, name: string) => [
              value,
              name === 'inFlight' ? 'In-flight' : name,
            ]}
          />
          <Line
            type="monotone"
            dataKey="inFlight"
            name="inFlight"
            stroke="#E8581C"
            strokeWidth={1}
            dot={false}
            activeDot={{ r: 2, fill: '#E8581C', stroke: '#F8F8F8' }}
            isAnimationActive={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}
