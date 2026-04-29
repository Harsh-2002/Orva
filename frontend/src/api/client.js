import axios from 'axios'

// API key: optionally set by the user via localStorage (for CLI-style access
// from the UI). Normally the UI authenticates via the session cookie set by
// /auth/login — no header needed.
function getApiKey() {
  return localStorage.getItem('orva_api_key') || ''
}

const apiClient = axios.create({
  baseURL: '/api/v1',
  timeout: 60000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

apiClient.interceptors.request.use((config) => {
  const key = getApiKey()
  if (key) {
    config.headers['X-Orva-API-Key'] = key
  }
  return config
})

apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response) {
      console.error('API Error:', error.response.data)
    } else if (error.request) {
      console.error('Network Error:', error.message)
    }
    return Promise.reject(error)
  }
)

// fnClient is used exclusively for /fn/ invocations. It lives at a
// separate baseURL because /fn/ is not under /api/v1.
const fnClient = axios.create({
  baseURL: '/fn',
  timeout: 60000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

fnClient.interceptors.request.use((config) => {
  const key = getApiKey()
  if (key) {
    config.headers['X-Orva-API-Key'] = key
  }
  return config
})

export default apiClient

export { getApiKey, fnClient }
