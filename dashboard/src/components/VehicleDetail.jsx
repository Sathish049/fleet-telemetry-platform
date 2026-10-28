import { useState } from 'react'
import { CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { api } from '../api'
import { usePolling } from '../usePolling'
import { clock, timeAgo } from '../format'

function Chart({ data, dataKey, label, unit, tickUnit = unit, color, domain, threshold }) {
  return (
    <div className="chart">
      <div className="chart-title">{label}</div>
      <ResponsiveContainer width="100%" height={170}>
        <LineChart data={data} margin={{ top: 5, right: 12, left: 0, bottom: 0 }}>
          <CartesianGrid stroke="var(--grid)" vertical={false} />
          <XAxis dataKey="ts" tickFormatter={clock} minTickGap={50} tick={{ fontSize: 11 }} />
          <YAxis domain={domain} tick={{ fontSize: 11 }} width={62} tickFormatter={(v) => `${Math.round(v)}${tickUnit}`} />
          <Tooltip labelFormatter={clock} formatter={(v) => [`${Number(v).toFixed(1)}${unit}`, label]} />
          {threshold != null && <ReferenceLine y={threshold} stroke="#e5484d" strokeDasharray="4 4" />}
          <Line type="monotone" dataKey={dataKey} stroke={color} strokeWidth={2} dot={false} isAnimationActive={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

export default function VehicleDetail({ vehicle, vehicleId, onClose }) {
  const [minutes, setMinutes] = useState(15)
  const history = usePolling(() => api.history(vehicleId, minutes), 3000, [vehicleId, minutes])
  const data = history.data || []

  return (
    <section className="card detail">
      <div className="detail-head">
        <div>
          <h2>{vehicleId}</h2>
          {vehicle && (
            <p className="muted">
              {vehicle.status} · {vehicle.speed_kph.toFixed(0)} km/h · battery {vehicle.battery_pct.toFixed(1)}% ·{' '}
              {vehicle.temp_c.toFixed(1)}°C · last seen {timeAgo(vehicle.ts)}
            </p>
          )}
        </div>
        <div className="detail-actions">
          {[5, 15, 60].map((m) => (
            <button key={m} className={m === minutes ? 'active' : ''} onClick={() => setMinutes(m)}>{m}m</button>
          ))}
          <button onClick={onClose}>Close</button>
        </div>
      </div>
      {data.length === 0 ? (
        <p className="muted">No readings in the last {minutes} minutes.</p>
      ) : (
        <div className="charts">
          <Chart data={data} dataKey="battery_pct" label="Battery" unit="%" color="#2fb170" domain={[0, 100]} threshold={20} />
          <Chart data={data} dataKey="temp_c" label="Temperature" unit="°C" color="#f5a524" domain={['auto', 'auto']} threshold={60} />
          <Chart data={data} dataKey="speed_kph" label="Speed (km/h)" unit=" km/h" tickUnit="" color="#1f6feb" domain={[0, 'auto']} />
        </div>
      )}
    </section>
  )
}
