import { useMemo, useState } from 'react'
import { batteryColor, timeAgo } from '../format'

const COLUMNS = [
  ['vehicle_id', 'Vehicle'],
  ['status', 'Status'],
  ['battery_pct', 'Battery'],
  ['speed_kph', 'Speed'],
  ['temp_c', 'Temp'],
  ['open_alerts', 'Alerts'],
  ['ts', 'Last seen'],
]

export default function VehicleTable({ vehicles, selected, onSelect }) {
  const [sort, setSort] = useState({ key: 'battery_pct', asc: true })
  const [query, setQuery] = useState('')

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const filtered = q ? vehicles.filter((v) => v.vehicle_id.toLowerCase().includes(q) || v.status.includes(q)) : vehicles
    return [...filtered].sort((a, b) => {
      const x = a[sort.key], y = b[sort.key]
      const cmp = x < y ? -1 : x > y ? 1 : 0
      return sort.asc ? cmp : -cmp
    })
  }, [vehicles, sort, query])

  return (
    <section className="card">
      <div className="table-head">
        <h2>Vehicles <span className="count">{vehicles.length}</span></h2>
        <input placeholder="Filter by ID or status…" value={query} onChange={(e) => setQuery(e.target.value)} />
      </div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              {COLUMNS.map(([key, label]) => (
                <th key={key} onClick={() => setSort((s) => ({ key, asc: s.key === key ? !s.asc : true }))}>
                  {label}{sort.key === key ? (sort.asc ? ' ▲' : ' ▼') : ''}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((v) => (
              <tr key={v.vehicle_id} className={v.vehicle_id === selected ? 'selected' : ''} onClick={() => onSelect(v.vehicle_id)}>
                <td><strong>{v.vehicle_id}</strong></td>
                <td><span className={`status status-${v.status}`}>{v.status}</span></td>
                <td>
                  <span className="bar"><span style={{ width: `${v.battery_pct}%`, background: batteryColor(v.battery_pct, v.status) }} /></span>
                  {v.battery_pct.toFixed(1)}%
                </td>
                <td>{v.speed_kph.toFixed(0)} km/h</td>
                <td className={v.temp_c > 60 ? 'hot' : ''}>{v.temp_c.toFixed(1)}°C</td>
                <td>{v.open_alerts || ''}</td>
                <td className="muted">{timeAgo(v.ts)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
