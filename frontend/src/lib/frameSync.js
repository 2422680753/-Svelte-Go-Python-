import { writable, derived } from 'svelte/store'
import { taskAPI } from './api'

export const FRAME_PAGE_SIZE = 20
export const PRELOAD_WINDOW = 5
export const VIRTUAL_BUFFER = 3

export function createFrameStore(taskId) {
  const frames = writable([])
  const frameMapping = writable(null)
  const currentTime = writable(0)
  const currentFrameIndex = writable(0)
  const isSyncing = writable(false)
  const error = writable(null)
  
  const loadedSegments = writable(new Set())
  const preloadedFrames = writable(new Map())
  
  const currentFrame = derived(
    [frames, currentFrameIndex],
    ([$frames, $index]) => $frames[$index] || null
  )
  
  const frameTimeRange = derived(
    [frameMapping, currentTime],
    ([$mapping, $time]) => {
      if (!$mapping || !$mapping.indexToTime) return null
      
      const indices = Object.keys($mapping.indexToTime).map(Number).sort((a, b) => a - b)
      if (indices.length === 0) return null
      
      for (let i = 0; i < indices.length - 1; i++) {
        const startTime = $mapping.indexToTime[indices[i]]
        const endTime = $mapping.indexToTime[indices[i + 1]]
        
        if ($time >= startTime && $time < endTime) {
          return {
            frameIndex: indices[i],
            startTime,
            endTime,
            nextFrameIndex: indices[i + 1]
          }
        }
      }
      
      const lastIndex = indices[indices.length - 1]
      return {
        frameIndex: lastIndex,
        startTime: $mapping.indexToTime[lastIndex],
        endTime: $mapping.duration,
        nextFrameIndex: null
      }
    }
  )
  
  async function loadFrameMapping() {
    try {
      const response = await fetch(`/api/tasks/${taskId}/frame-mapping`)
      if (response.ok) {
        const data = await response.json()
        frameMapping.set(data)
        return data
      }
    } catch (err) {
      error.set(err)
    }
    return null
  }
  
  async function loadFramesPage(page, pageSize = FRAME_PAGE_SIZE) {
    try {
      const response = await fetch(
        `/api/tasks/${taskId}/frames?page=${page}&page_size=${pageSize}`
      )
      if (response.ok) {
        const data = await response.json()
        return data.frames || []
      }
    } catch (err) {
      error.set(err)
    }
    return []
  }
  
  async function loadFramesByTimeRange(startTime, endTime) {
    try {
      const response = await fetch(
        `/api/tasks/${taskId}/frames-by-time?start=${startTime}&end=${endTime}`
      )
      if (response.ok) {
        const data = await response.json()
        return data.frames || []
      }
    } catch (err) {
      error.set(err)
    }
    return []
  }
  
  async function findFrameByTime(timestamp) {
    try {
      const response = await fetch(
        `/api/tasks/${taskId}/frames-by-time?time=${timestamp}`
      )
      if (response.ok) {
        const data = await response.json()
        return data.frame
      }
    } catch (err) {
      error.set(err)
    }
    return null
  }
  
  function updateTime(time) {
    currentTime.set(time)
    isSyncing.set(true)
    
    const $mapping = frameMapping.get()
    if ($mapping && $mapping.timeToIndex) {
      const times = Object.keys($mapping.timeToIndex).map(Number).sort((a, b) => a - b)
      
      let bestIndex = 0
      for (let i = 0; i < times.length; i++) {
        if (times[i] <= time) {
          bestIndex = $mapping.timeToIndex[times[i]]
        } else {
          break
        }
      }
      
      currentFrameIndex.set(bestIndex)
      
      preloadAroundIndex(bestIndex)
    }
    
    setTimeout(() => isSyncing.set(false), 50)
  }
  
  function updateFrameIndex(index) {
    currentFrameIndex.set(index)
    isSyncing.set(true)
    
    const $mapping = frameMapping.get()
    if ($mapping && $mapping.indexToTime && $mapping.indexToTime[index] !== undefined) {
      currentTime.set($mapping.indexToTime[index])
    }
    
    preloadAroundIndex(index)
    
    setTimeout(() => isSyncing.set(false), 50)
  }
  
  async function preloadAroundIndex(index) {
    const $preloaded = preloadedFrames.get()
    const startIndex = Math.max(0, index - PRELOAD_WINDOW)
    const endIndex = index + PRELOAD_WINDOW
    
    const indicesToLoad = []
    for (let i = startIndex; i <= endIndex; i++) {
      if (!$preloaded.has(i)) {
        indicesToLoad.push(i)
      }
    }
    
    if (indicesToLoad.length === 0) return
    
    const $mapping = frameMapping.get()
    if (!$mapping) return
    
    const times = indicesToLoad
      .filter(i => $mapping.indexToTime[i] !== undefined)
      .map(i => $mapping.indexToTime[i])
    
    if (times.length > 0) {
      const minTime = Math.min(...times)
      const maxTime = Math.max(...times)
      
      const frames = await loadFramesByTimeRange(minTime - 1, maxTime + 1)
      
      const newPreloaded = new Map($preloaded)
      frames.forEach(frame => {
        newPreloaded.set(frame.frame_index, frame)
      })
      preloadedFrames.set(newPreloaded)
      
      frames.update($frames => {
        const frameMap = new Map($frames.map(f => [f.frame_index, f]))
        frames.forEach(f => frameMap.set(f.frame_index, f))
        return Array.from(frameMap.values()).sort((a, b) => a.frame_index - b.frame_index)
      })
    }
  }
  
  async function initialize() {
    const mapping = await loadFrameMapping()
    if (mapping) {
      const initialFrames = await loadFramesPage(1, FRAME_PAGE_SIZE * 2)
      frames.set(initialFrames)
      
      const preloaded = new Map()
      initialFrames.forEach(f => preloaded.set(f.frame_index, f))
      preloadedFrames.set(preloaded)
    }
  }
  
  function seekToTime(time, videoElement = null) {
    updateTime(time)
    
    if (videoElement && !isNaN(videoElement.duration)) {
      const clampedTime = Math.max(0, Math.min(time, videoElement.duration))
      videoElement.currentTime = clampedTime
    }
  }
  
  function seekToFrame(index, videoElement = null) {
    updateFrameIndex(index)
    
    const $mapping = frameMapping.get()
    if (videoElement && $mapping && $mapping.indexToTime && $mapping.indexToTime[index] !== undefined) {
      const time = $mapping.indexToTime[index]
      if (!isNaN(videoElement.duration)) {
        const clampedTime = Math.max(0, Math.min(time, videoElement.duration))
        videoElement.currentTime = clampedTime
      }
    }
  }
  
  function getFrameForTime(time) {
    const $mapping = frameMapping.get()
    if (!$mapping || !$mapping.timeToIndex) return null
    
    const times = Object.keys($mapping.timeToIndex).map(Number).sort((a, b) => a - b)
    if (times.length === 0) return null
    
    for (let i = times.length - 1; i >= 0; i--) {
      if (times[i] <= time) {
        const frameIndex = $mapping.timeToIndex[times[i]]
        const $frames = frames.get()
        const $preloaded = preloadedFrames.get()
        
        return $frames.find(f => f.frame_index === frameIndex) || 
               $preloaded.get(frameIndex) ||
               { frame_index: frameIndex, timestamp: times[i] }
      }
    }
    
    return null
  }
  
  function getTimeForFrame(index) {
    const $mapping = frameMapping.get()
    if (!$mapping || !$mapping.indexToTime) return null
    
    return $mapping.indexToTime[index]
  }
  
  return {
    frames,
    frameMapping,
    currentTime,
    currentFrameIndex,
    currentFrame,
    isSyncing,
    error,
    loadedSegments,
    preloadedFrames,
    frameTimeRange,
    
    initialize,
    loadFrameMapping,
    loadFramesPage,
    loadFramesByTimeRange,
    findFrameByTime,
    
    updateTime,
    updateFrameIndex,
    preloadAroundIndex,
    
    seekToTime,
    seekToFrame,
    
    getFrameForTime,
    getTimeForFrame,
  }
}

export function createVirtualScrollStore(frameStore, options = {}) {
  const {
    itemHeight = 120,
    itemWidth = 160,
    containerHeight = 400,
    containerWidth = 800,
    buffer = VIRTUAL_BUFFER
  } = options
  
  const scrollTop = writable(0)
  const scrollLeft = writable(0)
  const visibleRange = writable({ start: 0, end: 0 })
  const virtualItems = writable([])
  
  const totalFrames = derived(
    [frameStore.frameMapping],
    ([$mapping]) => $mapping?.frameCount || 0
  )
  
  const columnsPerRow = Math.floor(containerWidth / itemWidth)
  
  function calculateVisibleRange() {
    const $totalFrames = totalFrames.get()
    const $scrollTop = scrollTop.get()
    
    const startRow = Math.max(0, Math.floor($scrollTop / itemHeight) - buffer)
    const endRow = Math.ceil(($scrollTop + containerHeight) / itemHeight) + buffer
    
    const startIndex = startRow * columnsPerRow
    const endIndex = Math.min($totalFrames, endRow * columnsPerRow)
    
    visibleRange.set({ start: startIndex, end: endIndex })
    
    return { startIndex, endIndex, startRow }
  }
  
  async function updateVirtualItems() {
    const { startIndex, endIndex, startRow } = calculateVisibleRange()
    
    const $frames = frameStore.frames.get()
    const $preloaded = frameStore.preloadedFrames.get()
    
    const items = []
    for (let i = startIndex; i < endIndex; i++) {
      const row = Math.floor((i - startIndex) / columnsPerRow)
      const col = (i - startIndex) % columnsPerRow
      
      const frame = $frames.find(f => f.frame_index === i) || $preloaded.get(i)
      
      items.push({
        index: i,
        frame,
        style: {
          position: 'absolute',
          top: `${(startRow + row) * itemHeight}px`,
          left: `${col * itemWidth}px`,
          width: `${itemWidth}px`,
          height: `${itemHeight}px`
        }
      })
    }
    
    virtualItems.set(items)
    
    const $mapping = frameStore.frameMapping.get()
    if ($mapping && $mapping.indexToTime) {
      const timesToLoad = []
      for (let i = startIndex; i < endIndex; i++) {
        if ($mapping.indexToTime[i] !== undefined) {
          timesToLoad.push($mapping.indexToTime[i])
        }
      }
      
      if (timesToLoad.length > 0) {
        const minTime = Math.min(...timesToLoad)
        const maxTime = Math.max(...timesToLoad)
        
        const loadedFrames = await frameStore.loadFramesByTimeRange(minTime - 0.5, maxTime + 0.5)
        
        if (loadedFrames.length > 0) {
          const frameMap = new Map($frames.map(f => [f.frame_index, f]))
          loadedFrames.forEach(f => frameMap.set(f.frame_index, f))
          
          frameStore.frames.set(Array.from(frameMap.values()).sort((a, b) => a.frame_index - b.frame_index))
          
          const newPreloaded = new Map($preloaded)
          loadedFrames.forEach(f => newPreloaded.set(f.frame_index, f))
          frameStore.preloadedFrames.set(newPreloaded)
        }
      }
    }
  }
  
  function handleScroll(event) {
    scrollTop.set(event.target.scrollTop)
    scrollLeft.set(event.target.scrollLeft)
    updateVirtualItems()
  }
  
  function scrollToFrame(index) {
    const row = Math.floor(index / columnsPerRow)
    const targetScrollTop = row * itemHeight
    
    scrollTop.set(targetScrollTop)
    updateVirtualItems()
    
    return targetScrollTop
  }
  
  function scrollToTime(time) {
    const $mapping = frameStore.frameMapping.get()
    if (!$mapping || !$mapping.timeToIndex) return null
    
    const times = Object.keys($mapping.timeToIndex).map(Number).sort((a, b) => a - b)
    let frameIndex = 0
    
    for (let i = 0; i < times.length; i++) {
      if (times[i] <= time) {
        frameIndex = $mapping.timeToIndex[times[i]]
      } else {
        break
      }
    }
    
    return scrollToFrame(frameIndex)
  }
  
  return {
    scrollTop,
    scrollLeft,
    visibleRange,
    virtualItems,
    totalFrames,
    columnsPerRow,
    itemHeight,
    itemWidth,
    
    calculateVisibleRange,
    updateVirtualItems,
    handleScroll,
    scrollToFrame,
    scrollToTime,
  }
}

export function createVideoFrameSync(videoElement, frameStore, virtualScroll = null) {
  let isPlaying = false
  let animationFrameId = null
  let lastSyncTime = 0
  const SYNC_INTERVAL = 100
  
  function onTimeUpdate() {
    const now = Date.now()
    if (now - lastSyncTime < SYNC_INTERVAL) return
    
    lastSyncTime = now
    const currentTime = videoElement.currentTime
    
    frameStore.updateTime(currentTime)
    
    if (virtualScroll) {
      const $mapping = frameStore.frameMapping.get()
      if ($mapping && $mapping.timeToIndex) {
        const times = Object.keys($mapping.timeToIndex).map(Number).sort((a, b) => a - b)
        let frameIndex = 0
        
        for (let i = 0; i < times.length; i++) {
          if (times[i] <= currentTime) {
            frameIndex = $mapping.timeToIndex[times[i]]
          } else {
            break
          }
        }
        
        virtualScroll.scrollToFrame(frameIndex)
      }
    }
  }
  
  function onPlay() {
    isPlaying = true
    startAnimationLoop()
  }
  
  function onPause() {
    isPlaying = false
    stopAnimationLoop()
  }
  
  function onSeeked() {
    onTimeUpdate()
  }
  
  function startAnimationLoop() {
    function loop() {
      if (!isPlaying) return
      
      onTimeUpdate()
      animationFrameId = requestAnimationFrame(loop)
    }
    
    animationFrameId = requestAnimationFrame(loop)
  }
  
  function stopAnimationLoop() {
    if (animationFrameId) {
      cancelAnimationFrame(animationFrameId)
      animationFrameId = null
    }
  }
  
  function attach() {
    videoElement.addEventListener('timeupdate', onTimeUpdate)
    videoElement.addEventListener('play', onPlay)
    videoElement.addEventListener('pause', onPause)
    videoElement.addEventListener('seeked', onSeeked)
  }
  
  function detach() {
    videoElement.removeEventListener('timeupdate', onTimeUpdate)
    videoElement.removeEventListener('play', onPlay)
    videoElement.removeEventListener('pause', onPause)
    videoElement.removeEventListener('seeked', onSeeked)
    stopAnimationLoop()
  }
  
  return {
    attach,
    detach,
    onTimeUpdate,
    onPlay,
    onPause,
    onSeeked,
  }
}

export function formatDuration(seconds) {
  if (!seconds || isNaN(seconds)) return '00:00'
  const mins = Math.floor(seconds / 60)
  const secs = Math.floor(seconds % 60)
  const ms = Math.floor((seconds % 1) * 1000)
  return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}.${String(ms).padStart(3, '0')}`
}

export function formatTimeShort(seconds) {
  if (!seconds || isNaN(seconds)) return '00:00'
  const mins = Math.floor(seconds / 60)
  const secs = Math.floor(seconds % 60)
  return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`
}
