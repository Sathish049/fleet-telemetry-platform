import { useState } from 'react'
import { api } from './api'
import { usePolling } from './usePolling'
import SummaryCards from './components/SummaryCards'
import FleetMap from './components/FleetMap'
import AlertsPanel from './components/AlertsPanel'
import VehicleDetail from './components/VehicleDetail'
import VehicleTable from './components/VehicleTable'

export default function App() {
  const [selected, setSelected] = useState(null)
  const summary = usePolling(api.summary, 2000)
  const vehicles = usePolling(api.vehicles, 2000)
  const alerts = usePolling(() => api.alerts('open'), 3000)

  const error = summary.error || vehicles.error || alerts.error

  return (
    <div className="app">
      <header className="topbar">
        <div>
          <h1>Fleet Telemetry Monitor</h1>
          <p className="muted">Live vehicle data · refreshes every 2 seconds</p>
        </div>
        <span className={`pill ${error ? 'pill-bad' : 'pill-ok'}`}>{error ? 'API unreachable' : 'Live'}</span>
      </header>

      {error && <div className="banner">Could not reach the API: {error}</div>}

      <SummaryCards data={summary.data} />

      <div className="grid-main">
        <section className="card map-card">
          <h2>Fleet map</h2>
          <FleetMap vehicles={vehicles.data || []} selected={selected} onSelect={setSelected} />
          <div className="legend">
            <span><i style={{ background: '#2fb170' }} /> Battery ≥ 50%</span>
            <span><i style={{ background: '#f5a524' }} /> 20–50%</span>
            <span><i style={{ background: '#e5484d' }} /> &lt; 20%</span>
            <span><i style={{ background: '#8a93a3' }} /> Offline</span>
            <span><i className="ring" /> Open alert</span>
          </div>
        </section>
        <AlertsPanel
          alerts={alerts.data || []}
          total={summary.data?.open_alerts}
          onSelect={setSelected}
          onAck={async (id) => { await api.ackAlert(id); alerts.refresh(); summary.refresh() }}
        />
      </div>

      {selected && (
        <VehicleDetail
          vehicle={(vehicles.data || []).find((v) => v.vehicle_id === selected)}
          vehicleId={selected}
          onClose={() => setSelected(null)}
        />
      )}

      <VehicleTable vehicles={vehicles.data || []} selected={selected} onSelect={setSelected} />
    </div>
  )
}
