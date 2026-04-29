import { writable, derived } from 'svelte/store'

export const user = writable(null)
export const isAuthenticated = writable(false)
export const currentTask = writable(null)
export const selectedTags = writable([])
export const notification = writable(null)

export const isAdmin = derived(user, ($user) => {
  return $user?.role === 'admin'
})

export const isSeniorReviewer = derived(user, ($user) => {
  return $user?.role === 'admin' || $user?.role === 'senior_reviewer'
})

export function showNotification(message, type = 'info') {
  notification.set({ message, type, id: Date.now() })
  setTimeout(() => {
    notification.set(null)
  }, 5000)
}

export function logout() {
  localStorage.removeItem('token')
  localStorage.removeItem('user')
  user.set(null)
  isAuthenticated.set(false)
}

export function login(token, userData) {
  localStorage.setItem('token', token)
  localStorage.setItem('user', JSON.stringify(userData))
  user.set(userData)
  isAuthenticated.set(true)
}
