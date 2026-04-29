<script>
  import { onMount, onDestroy } from 'svelte'
  import { createVirtualScrollStore, createVideoFrameSync, formatTimeShort } from '$lib/frameSync'
  import { AlertTriangle } from 'lucide-svelte'

  export let frames = []
  export let frameMapping = null
  export let selectedFrame = null
  export let currentFrameIndex = 0
  export let frameStore = null
  export let videoElement = null
  
  export let containerHeight = 300
  export let containerWidth = 800
  export let itemHeight = 130
  export let itemWidth = 180
  export let gap = 8
  
  let containerElement = null
  let virtualScroll = null
  let syncHandler = null
  let unsubscribeVirtual = null
  let totalHeight = 0

  $: {
    if (frameMapping && frameMapping.frameCount) {
      const columnsPerRow = Math.floor((containerWidth - gap) / (itemWidth + gap))
      const totalRows = Math.ceil(frameMapping.frameCount / columnsPerRow)
      totalHeight = totalRows * itemHeight
    }
  }

  onMount(() => {
    if (frameStore) {
      virtualScroll = createVirtualScrollStore(frameStore, {
        itemHeight,
        itemWidth,
        containerHeight,
        containerWidth
      })
      
      unsubscribeVirtual = virtualScroll.virtualItems.subscribe((items) => {
        // 触发更新
      })
      
      if (videoElement && frameStore) {
        syncHandler = createVideoFrameSync(videoElement, frameStore, virtualScroll)
        syncHandler.attach()
      }
    }
  })

  onDestroy(() => {
    if (syncHandler) {
      syncHandler.detach()
    }
    if (unsubscribeVirtual) {
      unsubscribeVirtual()
    }
  })

  function handleScroll(e) {
    if (virtualScroll) {
      virtualScroll.handleScroll(e)
    }
  }

  function handleFrameClick(frame) {
    if (frameStore && videoElement) {
      frameStore.seekToFrame(frame.frame_index, videoElement)
    }
    if (typeof onFrameSelect === 'function') {
      onFrameSelect(frame)
    }
  }

  function handleFrameDblClick(frame) {
    if (typeof onFrameDblClick === 'function') {
      onFrameDblClick(frame)
    }
  }

  export function scrollToFrame(index) {
    if (virtualScroll) {
      const scrollTop = virtualScroll.scrollToFrame(index)
      if (containerElement) {
        containerElement.scrollTop = scrollTop
      }
    }
  }

  export function scrollToTime(time) {
    if (virtualScroll) {
      const scrollTop = virtualScroll.scrollToTime(time)
      if (containerElement && scrollTop !== null) {
        containerElement.scrollTop = scrollTop
      }
    }
  }

  $: virtualItems = $virtualScroll?.virtualItems || []
  $: visibleRange = $virtualScroll?.visibleRange || { start: 0, end: 0 }
</script>

<div class="virtual-frame-gallery">
  {#if frames.length === 0 && frameMapping?.frameCount > 0}
    <div class="loading-frames">
      <div class="loading-text">加载帧中...</div>
      <div class="spinner"></div>
    </div>
  {:else if frames.length === 0}
    <div class="empty-frames">
      <div class="empty-text">暂无视频帧</div>
    </div>
  {:else}
    <div 
      class="frame-container"
      bind:this={containerElement}
      style="height: {containerHeight}px; width: {containerWidth}px"
      on:scroll={handleScroll}
    >
      <div class="frame-spacer" style="height: {totalHeight}px; width: 100%">
        {#each $virtualScroll?.virtualItems || [] as item}
          <div
            class="virtual-frame-item"
            style={item.style}
            on:click={() => handleFrameClick(item.frame)}
            on:dblclick={() => handleFrameDblClick(item.frame)}
            class:selected={item.frame?.frame_index === selectedFrame?.frame_index}
            class:current={item.frame?.frame_index === $currentFrameIndex}
            class:flagged={item.frame?.is_flagged}
          >
            {#if item.frame?.frame_url}
              <img 
                src={item.frame.frame_url} 
                alt="帧 {item.frame.frame_index}"
                loading="lazy"
              />
            {:else}
              <div class="frame-placeholder">
                <span>帧 {item.index}</span>
              </div>
            {/if}
            <div class="frame-info">
              <span class="frame-index">#{item.index}</span>
              {#if item.frame?.timestamp !== undefined}
                <span class="frame-time">{formatTimeShort(item.frame.timestamp)}</span>
              {/if}
            </div>
            {#if item.frame?.is_flagged}
              <div class="flag-indicator">
                <AlertTriangle size={12} color="white" />
              </div>
            {/if}
            {#if item.frame?.frame_index === $currentFrameIndex}
              <div class="current-indicator"></div>
            {/if}
          </div>
        {/each}
      </div>
    </div>
    
    {#if frameMapping?.frameCount > 0}
      <div class="frame-timeline">
        <div class="timeline-info">
          <span class="frame-count">共 {frameMapping.frameCount} 帧</span>
          {#if frameMapping.frameInterval > 0}
            <span class="frame-interval">帧间隔: {frameMapping.frameInterval.toFixed(1)}s</span>
          {/if}
        </div>
        <div class="timeline-bar">
          <div class="timeline-progress" style="width: {($currentFrameIndex / frameMapping.frameCount * 100}%"></div>
        </div>
        <div class="timeline-times">
          <span>00:00</span>
          <span>{formatTimeShort(frameMapping.duration)}</span>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .virtual-frame-gallery {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .frame-container {
    overflow-y: auto;
    overflow-x: hidden;
    position: relative;
    border: 1px solid #e5e7eb;
    border-radius: 8px;
    background: #f9fafb;
  }

  .frame-spacer {
    position: relative;
  }

  .virtual-frame-item {
    position: absolute;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 4px;
    cursor: pointer;
    border-radius: 6px;
    transition: all 0.15s ease;
    box-sizing: border-box;
  }

  .virtual-frame-item:hover {
    background: rgba(59, 130, 246, 0.1);
    transform: scale(1.02);
  }

  .virtual-frame-item.selected {
    background: rgba(59, 130, 246, 0.2);
    border: 2px solid #3b82f6;
  }

  .virtual-frame-item.current {
    border: 2px solid #10b981;
  }

  .virtual-frame-item.flagged {
    background: rgba(239, 68, 68, 0.1);
  }

  .virtual-frame-item img {
    max-width: 100%;
    max-height: 90px;
    object-fit: contain;
    border-radius: 4px;
    background: #e5e7eb;
  }

  .frame-placeholder {
    width: 100%;
    height: 90px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #e5e7eb;
    border-radius: 4px;
    color: #6b7280;
    font-size: 12px;
  }

  .frame-info {
    display: flex;
    justify-content: space-between;
    width: 100%;
    padding: 4px 0;
    font-size: 10px;
    color: #6b7280;
  }

  .frame-index {
    font-weight: 500;
    color: #374151;
  }

  .flag-indicator {
    position: absolute;
    top: 8px;
    right: 8px;
    background: #ef4444;
    border-radius: 50%;
    padding: 2px;
  }

  .current-indicator {
    position: absolute;
    bottom: 28px;
    left: 50%;
    transform: translateX(-50%);
    width: 8px;
    height: 8px;
    background: #10b981;
    border-radius: 50%;
    box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.4);
  }

  .loading-frames,
  .empty-frames {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 40px;
    color: #6b7280;
    font-size: 14px;
  }

  .frame-timeline {
    padding: 8px;
    background: #f3f4f6;
    border-radius: 6px;
  }

  .timeline-info {
    display: flex;
    justify-content: space-between;
    font-size: 12px;
    color: #6b7280;
    margin-bottom: 8px;
  }

  .timeline-bar {
    height: 6px;
    background: #e5e7eb;
    border-radius: 3px;
    overflow: hidden;
  }

  .timeline-progress {
    height: 100%;
    background: linear-gradient(90deg, #3b82f6, #60a5fa);
    border-radius: 3px;
    transition: width 0.1s ease;
  }

  .timeline-times {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
    color: #9ca3af;
    margin-top: 4px;
  }

  .spinner {
    width: 24px;
    height: 24px;
    border: 2px solid #e5e7eb;
    border-top-color: #3b82f6;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    margin-top: 8px;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
