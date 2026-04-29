import axios from 'axios'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
})

api.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('user')
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)

export const authAPI = {
  login: (data => api.post('/auth/login', data),
  register: (data) => api.post('/auth/register', data),
  getCurrentUser: () => api.get('/auth/me'),
}

export const taskAPI = {
  createVideo: (data) => api.post('/videos', data),
  getTasks: (params) => api.get('/tasks', { params }),
  getTask: (id) => api.get(`/tasks/${id}`),
  startReview: (id) => api.post(`/tasks/${id}/start-review`),
  submitReview: (id, data) => api.post(`/tasks/${id}/submit-review`, data),
  assignTask: (id, data) => api.post(`/tasks/${id}/assign`, data),
  getMyPending: () => api.get('/tasks/my-pending'),
  getMyHistory: (params) => api.get('/tasks/my-history', { params }),
  getInconsistentTasks: () => api.get('/tasks/inconsistent'),
  getTaskSnapshots: (id, params) => api.get(`/tasks/${id}/snapshots`, { params }),
  checkConsistency: (id) => api.get(`/tasks/${id}/consistency`),
  rollbackTask: (id, data) => api.post(`/tasks/${id}/rollback`, data),
  getFrameMapping: (id) => api.get(`/tasks/${id}/frame-mapping`),
  getFrames: (id, params) => api.get(`/tasks/${id}/frames`, { params }),
  getFramesByTime: (id, params) => api.get(`/tasks/${id}/frames-by-time`, { params }),
  getFlaggedFrames: (id) => api.get(`/tasks/${id}/flagged-frames`),
  getFrameProgress: (id) => api.get(`/tasks/${id}/frame-progress`),
}

export const batchAPI = {
  create: (data) => api.post('/batch', data),
  getStatus: (id) => api.get(`/batch/${id}`),
}

export const appealAPI = {
  submit: (data) => api.post('/appeals', data),
  getMyAppeals: (params) => api.get('/appeals/my', { params }),
  getPending: () => api.get('/appeals/pending'),
  getDetails: (id) => api.get(`/appeals/${id}`),
  assign: (id, data) => api.post(`/appeals/${id}/assign`, data),
  resolve: (id, data) => api.post(`/appeals/${id}/resolve`, data),
}

export const statsAPI = {
  getDashboard: () => api.get('/stats/dashboard'),
  getTrends: (params) => api.get('/stats/trends', { params }),
  getReviewerPerformance: (params) => api.get('/stats/reviewers', { params }),
  getViolationStats: (params) => api.get('/stats/violations', { params }),
  getSLAPerformance: (params) => api.get('/stats/sla', { params }),
}

export const tagsAPI = {
  getAll: () => api.get('/violation-tags'),
}

export default api
