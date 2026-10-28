export const batteryColor = (pct, status) => {
  if (status === 'offline') return '#8a93a3'
  if (pct < 20) return '#e5484d'
  if (pct < 50) return '#f5a524'
  return '#2fb170'
}

export const timeAgo = (iso) => {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  return `${Math.floor(s / 3600)}h ago`
}

export const clock = (iso) =>
  new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })

export const ALERT_LABELS = {
  HIGH_TEMP: 'High temperature',
  RAPID_DRAIN: 'Rapid battery drain',
  LOW_BATTERY: 'Low battery',
}
