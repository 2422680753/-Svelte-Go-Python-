import { writable, derived, get } from 'svelte/store'
import api, { taskAPI } from './api'
import { user, showNotification } from './store'

const TASK_CACHE_KEY = 'moderation_task_cache'
const PENDING_ACTIONS_KEY = 'pending_moderation_actions'

export const taskState = writable({
  task: null,
  autoResult: null,
  humanResult: null,
  snapshots: [],
  canRollback: false,
  rollbackReason: '',
  isConsistent: true,
  inconsistencies: null,
  frames: [],
  loading: false,
  error: null,
  lastSyncAt: null,
})

export const reviewSession = writable({
  isActive: false,
  taskId: null,
  startTime: null,
  elapsedTime: 0,
  isLocked: false,
  lockHolder: null,
  heartbeatInterval: null,
})

export const pendingActions = writable([])

export const isReviewing = derived(reviewSession, ($session) => $session.isActive)

export const currentReviewDuration = derived(reviewSession, ($session) => {
  if (!$session.startTime) return 0
  return Math.floor((Date.now() - $session.startTime) / 1000)
})

export async function loadTask(taskId) {
  taskState.update(s => ({ ...s, loading: true, error: null }))
  
  try {
    const response = await taskAPI.getTask(taskId)
    const data = response.data
    
    taskState.set({
      task: data.task,
      autoResult: data.auto_result,
      humanResult: data.human_result,
      snapshots: data.snapshot_history || [],
      canRollback: data.can_rollback || false,
      rollbackReason: data.rollback_reason || '',
      isConsistent: true,
      inconsistencies: null,
      frames: data.frames || [],
      loading: false,
      error: null,
      lastSyncAt: new Date(),
    })
    
    saveTaskToCache(data)
    return data
  } catch (error) {
    const errorMessage = error.response?.data?.error || error.message || 'Failed to load task'
    
    const cachedTask = loadTaskFromCache(taskId)
    if (cachedTask) {
      showNotification('Using cached task data - network error', 'warning')
      taskState.update(s => ({
        ...s,
        loading: false,
        error: errorMessage,
      }))
      return cachedTask
    }
    
    taskState.update(s => ({
      ...s,
      loading: false,
      error: errorMessage,
    }))
    throw error
  }
}

export async function startReview(taskId) {
  try {
    const response = await taskAPI.startReview(taskId)
    
    startReviewSession(taskId)
    
    await loadTask(taskId)
    
    showNotification('Review started successfully', 'success')
    return response.data
  } catch (error) {
    const errorMessage = error.response?.data?.error || error.message
    showNotification(`Failed to start review: ${errorMessage}`, 'error')
    throw error
  }
}

export async function submitReview(taskId, decision, violationTags, comment) {
  const reviewDuration = get(currentReviewDuration)
  
  const pendingAction = {
    id: Date.now().toString(),
    type: 'submit_review',
    taskId,
    decision,
    violationTags,
    comment,
    reviewDuration: reviewDuration > 0 ? reviewDuration : 60,
    createdAt: new Date().toISOString(),
    status: 'pending',
  }
  
  addPendingAction(pendingAction)
  
  try {
    const response = await taskAPI.submitReview(taskId, {
      decision,
      violation_tags: violationTags,
      comment,
      review_duration: reviewDuration > 0 ? reviewDuration : 60,
    })
    
    removePendingAction(pendingAction.id)
    stopReviewSession()
    
    await loadTask(taskId)
    
    showNotification(`Review submitted: ${decision === 'approve' ? 'Approved' : 'Rejected'}`, 'success')
    return response.data
  } catch (error) {
    updatePendingActionStatus(pendingAction.id, 'failed', error.message)
    
    const errorMessage = error.response?.data?.error || error.message
    showNotification(`Failed to submit review: ${errorMessage}. Action saved for retry.`, 'error')
    
    throw error
  }
}

export async function rollbackTask(taskId, reason) {
  try {
    const response = await api.post(`/tasks/${taskId}/rollback`, { reason })
    
    await loadTask(taskId)
    
    showNotification('Task rolled back successfully', 'success')
    return response.data
  } catch (error) {
    const errorMessage = error.response?.data?.error || error.message
    showNotification(`Failed to rollback: ${errorMessage}`, 'error')
    throw error
  }
}

export async function checkTaskConsistency(taskId) {
  try {
    const response = await api.get(`/tasks/${taskId}/consistency`)
    const data = response.data
    
    taskState.update(s => ({
      ...s,
      isConsistent: data.is_consistent,
      inconsistencies: data.inconsistencies,
    }))
    
    if (!data.is_consistent) {
      showNotification('Task inconsistency detected', 'warning')
    }
    
    return data
  } catch (error) {
    const errorMessage = error.response?.data?.error || error.message
    taskState.update(s => ({
      ...s,
      isConsistent: true,
      inconsistencies: null,
    }))
    showNotification(`Failed to check consistency: ${errorMessage}`, 'error')
    throw error
  }
}

export async function getTaskSnapshots(taskId, limit = 20) {
  try {
    const response = await api.get(`/tasks/${taskId}/snapshots`, { params: { limit } })
    const data = response.data
    
    taskState.update(s => ({
      ...s,
      snapshots: data.snapshots || [],
    }))
    
    return data
  } catch (error) {
    const errorMessage = error.response?.data?.error || error.message
    showNotification(`Failed to load snapshots: ${errorMessage}`, 'error')
    throw error
  }
}

function startReviewSession(taskId) {
  reviewSession.set({
    isActive: true,
    taskId,
    startTime: Date.now(),
    elapsedTime: 0,
    isLocked: true,
    lockHolder: get(user)?.id,
    heartbeatInterval: setInterval(sendHeartbeat, 30000),
  })
}

function stopReviewSession() {
  const session = get(reviewSession)
  if (session.heartbeatInterval) {
    clearInterval(session.heartbeatInterval)
  }
  
  reviewSession.set({
    isActive: false,
    taskId: null,
    startTime: null,
    elapsedTime: 0,
    isLocked: false,
    lockHolder: null,
    heartbeatInterval: null,
  })
}

async function sendHeartbeat() {
  const session = get(reviewSession)
  if (!session.isActive || !session.taskId) return
  
  try {
    await api.post(`/tasks/${session.taskId}/heartbeat`, {
      reviewer_id: session.lockHolder,
    })
  } catch (error) {
    console.error('Heartbeat failed:', error)
  }
}

function addPendingAction(action) {
  pendingActions.update(actions => [...actions, action])
  savePendingActions()
}

function removePendingAction(id) {
  pendingActions.update(actions => actions.filter(a => a.id !== id))
  savePendingActions()
}

function updatePendingActionStatus(id, status, error) {
  pendingActions.update(actions => 
    actions.map(a => 
      a.id === id 
        ? { ...a, status, error, lastAttemptAt: new Date().toISOString() }
        : a
    )
  )
  savePendingActions()
}

function savePendingActions() {
  const actions = get(pendingActions)
  if (actions.length > 0) {
    localStorage.setItem(PENDING_ACTIONS_KEY, JSON.stringify(actions))
  } else {
    localStorage.removeItem(PENDING_ACTIONS_KEY)
  }
}

function loadPendingActions() {
  const saved = localStorage.getItem(PENDING_ACTIONS_KEY)
  if (saved) {
    try {
      const actions = JSON.parse(saved)
      pendingActions.set(actions)
      return actions
    } catch (e) {
      console.error('Failed to load pending actions:', e)
    }
  }
  return []
}

export async function retryPendingActions() {
  const actions = get(pendingActions).filter(a => a.status === 'failed' || a.status === 'pending')
  
  for (const action of actions) {
    try {
      if (action.type === 'submit_review') {
        await submitReview(
          action.taskId,
          action.decision,
          action.violationTags,
          action.comment
        )
        updatePendingActionStatus(action.id, 'success', null)
      }
    } catch (error) {
      updatePendingActionStatus(action.id, 'failed', error.message)
    }
  }
}

function saveTaskToCache(data) {
  if (!data?.task) return
  
  try {
    const cache = JSON.parse(localStorage.getItem(TASK_CACHE_KEY) || '{}')
    cache[data.task.id] = {
      ...data,
      cachedAt: new Date().toISOString(),
    }
    localStorage.setItem(TASK_CACHE_KEY, JSON.stringify(cache))
  } catch (e) {
    console.error('Failed to cache task:', e)
  }
}

function loadTaskFromCache(taskId) {
  try {
    const cache = JSON.parse(localStorage.getItem(TASK_CACHE_KEY) || '{}')
    const cached = cache[taskId]
    if (cached) {
      const cachedAt = new Date(cached.cachedAt)
      if (Date.now() - cachedAt.getTime() < 3600000) {
        return cached
      }
    }
  } catch (e) {
    console.error('Failed to load cached task:', e)
  }
  return null
}

export function clearTaskCache() {
  localStorage.removeItem(TASK_CACHE_KEY)
  taskState.set({
    task: null,
    autoResult: null,
    humanResult: null,
    snapshots: [],
    canRollback: false,
    rollbackReason: '',
    isConsistent: true,
    inconsistencies: null,
    frames: [],
    loading: false,
    error: null,
    lastSyncAt: null,
  })
}

loadPendingActions()
