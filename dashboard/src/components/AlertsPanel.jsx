import { ALERT_LABELS, timeAgo } from '../format'

export default function AlertsPanel({ alerts, total, onSelect, onAck }) {
  return (
    <section className="card alerts">
      <h2>Open alerts <span className="count">{total ?? alerts.length}</span></h2>
      {alerts.length === 0 && <p className="muted">No open alerts. The fleet looks healthy.</p>}
      <ul>
        {alerts.map((a) => (
          <li key={a.id} className={`alert alert-${a.alert_type.toLowerCase()}`}>
            <div className="alert-main" onClick={() => onSelect(a.vehicle_id)}>
              <div className="alert-title">
                <strong>{a.vehicle_id}</strong> · {ALERT_LABELS[a.alert_type] || a.alert_type}
              </div>
              <div className="muted small">{a.message} · {timeAgo(a.ts)}</div>
            </div>
            <button onClick={() => onAck(a.id)} title="Acknowledge">Ack</button>
          </li>
        ))}
      </ul>
    </section>
  )
}
