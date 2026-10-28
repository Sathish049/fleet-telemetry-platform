// Thin wrapper around the FastAPI endpoints.
async function request(path, options) {
  const res = await fetch(path, options)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} for ${path}`)
  return res.json()
}

export const api = {
  summary: () => request('/api/fleet/summary'),
  vehicles: () => request('/api/vehicles'),
  history: (id, minutes = 15) => request(`/api/vehicles/${encodeURIComponent(id)}/history?minutes=${minutes}&max_points=300`),
  alerts: (status = 'open') => request(`/api/alerts?status=${status}&limit=100`),
  ackAlert: (id) => request(`/api/alerts/${id}/ack`, { method: 'POST' }),
}
