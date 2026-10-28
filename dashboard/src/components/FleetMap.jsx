import { CircleMarker, MapContainer, TileLayer, Tooltip } from 'react-leaflet'
import { batteryColor } from '../format'

const CENTER = [37.5485, -121.9886] // Fremont, CA

export default function FleetMap({ vehicles, selected, onSelect }) {
  return (
    <MapContainer center={CENTER} zoom={11} className="map" preferCanvas>
      <TileLayer
        attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
        url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
      />
      {vehicles.map((v) => {
        const isSel = v.vehicle_id === selected
        return (
          <CircleMarker
            key={v.vehicle_id}
            center={[v.lat, v.lng]}
            radius={isSel ? 9 : 6}
            pathOptions={{
              color: v.open_alerts > 0 ? '#e5484d' : isSel ? '#1f6feb' : '#ffffff',
              weight: v.open_alerts > 0 || isSel ? 3 : 1,
              fillColor: batteryColor(v.battery_pct, v.status),
              fillOpacity: 0.9,
            }}
            eventHandlers={{ click: () => onSelect(v.vehicle_id) }}
          >
            <Tooltip>
              <strong>{v.vehicle_id}</strong> · {v.status}<br />
              {v.battery_pct.toFixed(1)}% · {v.speed_kph.toFixed(0)} km/h · {v.temp_c.toFixed(1)}°C
            </Tooltip>
          </CircleMarker>
        )
      })}
    </MapContainer>
  )
}
