import api from './client'

export const historyRequest = (op, raw, signal) =>
  api.post(`/work-history/${op}`, raw, { signal, headers: { 'Content-Type': 'application/json' } })
