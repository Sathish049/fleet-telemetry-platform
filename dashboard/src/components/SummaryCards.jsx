const Stat = ({ label, value, tone }) => (
  <div className={`card stat ${tone || ''}`}>
    <div className="stat-label">{label}</div>
    <div className="stat-value">{value ?? '–'}</div>
  </div>
)

export default function SummaryCards({ data }) {
  const d = data || {}
  return (
    <div className="stats">
      <Stat label="Vehicles online" value={data ? `${d.online_vehicles} / ${d.total_vehicles}` : null} />
      <Stat label="Driving now" value={d.driving_vehicles} />
      <Stat label="Avg battery" value={d.avg_battery_pct != null ? `${d.avg_battery_pct}%` : null} />
      <Stat label="Low battery (<20%)" value={d.low_battery_vehicles} tone={d.low_battery_vehicles ? 'warn' : ''} />
      <Stat label="Open alerts" value={d.open_alerts} tone={d.open_alerts ? 'bad' : ''} />
      <Stat label="Readings / min" value={d.readings_last_minute?.toLocaleString()} />
    </div>
  )
}
