<script>
  import { onMount, onDestroy } from 'svelte'
  import { useNavigate, params } from 'svelte-routing'
  import { taskAPI, tagsAPI } from '$lib/api'
  import { showNotification, user } from '$lib/store'
  import { 
    Play, 
    Pause, 
    Check, 
    X, 
    Clock, 
    Tag, 
    AlertTriangle,
    ChevronLeft,
    Info,
    Download,
    Share2
  } from 'lucide-svelte'
  import dayjs from 'dayjs'

  const navigate = useNavigate()

  let taskId = $params.id
  let task = null
  let frames = []
  let autoResult = null
  let humanResult = null
  let logs = []
  let loading = true
  let violationTags = []
  
  let selectedViolationTags = []
  let reviewComment = ''
  let isSubmitting = false
  
  let selectedFrame = null
  let reviewStartTime = null
  let isPlaying = false
  let currentTime = 0
  
  let showFlagModal = false
  let flagFrameIndex = null
  let flagComment = ''

  onMount(async () => {
    reviewStartTime = Date.now()
    
    try {
      const [taskRes, tagsRes] = await Promise.all([
        taskAPI.getTask(taskId),
        tagsAPI.getAll(),
      ])
      
      task = taskRes.data.task
      frames = taskRes.data.frames || []
      autoResult = taskRes.data.auto_result
      humanResult = taskRes.data.human_result
      logs = taskRes.data.moderation_logs || []
      violationTags = tagsRes.data
      
      if (task.current_status === 'assigned') {
        await taskAPI.startReview(taskId)
      }
    } catch (error) {
      showNotification('加载任务详情失败', 'error')
    } finally {
      loading = false
    }
  })

  function getStatusBadge(status) {
    const statusMap = {
      pending: { class: 'badge-secondary', label: '待处理' },
      auto_moderating: { class: 'badge-info', label: '机审中' },
      auto_approved: { class: 'badge-success', label: '机审通过' },
      auto_rejected: { class: 'badge-danger', label: '机审拒绝' },
      need_review: { class: 'badge-warning', label: '待人工审核' },
      assigned: { class: 'badge-primary', label: '已分配' },
      in_review: { class: 'badge-info', label: '审核中' },
      human_approved: { class: 'badge-success', label: '人工通过' },
      human_rejected: { class: 'badge-danger', label: '人工拒绝' },
      appealed: { class: 'badge-warning', label: '已申诉' },
      appeal_approved: { class: 'badge-success', label: '申诉通过' },
      appeal_rejected: { class: 'badge-danger', label: '申诉驳回' },
      published: { class: 'badge-success', label: '已发布' },
      banned: { class: 'badge-danger', label: '已封禁' },
    }
    return statusMap[status] || { class: 'badge-secondary', label: status }
  }

  function getPriorityBadge(priority) {
    const priorityMap = {
      high: { class: 'badge-danger', label: '高优先级' },
      normal: { class: 'badge-primary', label: '正常' },
      low: { class: 'badge-secondary', label: '低优先级' },
    }
    return priorityMap[priority] || { class: 'badge-secondary', label: priority }
  }

  function formatTime(timestamp) {
    if (!timestamp) return '-'
    return dayjs(timestamp).format('YYYY-MM-DD HH:mm:ss')
  }

  function formatDuration(seconds) {
    if (!seconds) return '00:00'
    const mins = Math.floor(seconds / 60)
    const secs = Math.floor(seconds % 60)
    return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`
  }

  function toggleViolationTag(tagName) {
    if (selectedViolationTags.includes(tagName)) {
      selectedViolationTags = selectedViolationTags.filter(t => t !== tagName)
    } else {
      selectedViolationTags = [...selectedViolationTags, tagName]
    }
  }

  async function submitReview(decision) {
    if (decision === 'reject' && selectedViolationTags.length === 0) {
      showNotification('请至少选择一个违规标签', 'warning')
      return
    }

    isSubmitting = true
    
    try {
      const reviewDuration = (Date.now() - reviewStartTime) / 1000
      
      await taskAPI.submitReview(taskId, {
        decision,
        violation_tags: selectedViolationTags,
        comment: reviewComment,
        review_duration: reviewDuration,
      })
      
      showNotification(decision === 'approve' ? '审核通过' : '审核拒绝', 'success')
      navigate('/my-tasks')
    } catch (error) {
      showNotification('提交审核失败', 'error')
    } finally {
      isSubmitting = false
    }
  }

  function selectFrame(frame) {
    selectedFrame = frame
  }

  function openFlagModal(frameIndex) {
    flagFrameIndex = frameIndex
    flagComment = ''
    showFlagModal = true
  }

  function flagFrame() {
    if (frames[flagFrameIndex]) {
      frames[flagFrameIndex].is_flagged = true
    }
    showFlagModal = false
  }

  function goBack() {
    navigate('/my-tasks')
  }

  $: canReview = task && ['in_review', 'assigned'].includes(task.current_status)
  $: isFinalState = task && ['human_approved', 'human_rejected', 'published', 'banned', 'appealed'].includes(task.current_status)
</script>

<div class="page-header">
  <div class="flex items-center gap-4">
    <button class="btn btn-secondary btn-sm" on:click={goBack}>
      <ChevronLeft size={16} />
      返回
    </button>
    <div>
      <h1 class="page-title">审核任务</h1>
      <p class="text-secondary text-sm mt-1">
        任务ID: {taskId.slice(0, 12)}...
        {#if task}
          <span class="ml-2 badge {getStatusBadge(task.current_status).class}">
            {getStatusBadge(task.current_status).label}
          </span>
          <span class="ml-2 badge {getPriorityBadge(task.priority).class}">
            {getPriorityBadge(task.priority).label}
          </span>
        {/if}
      </p>
    </div>
  </div>
</div>

<div class="page-content">
  {#if loading}
    <div class="loading-state">
      <div class="spinner"></div>
    </div>
  {:else if !task}
    <div class="empty-state">
      <AlertTriangle size={48} class="empty-icon" />
      <div class="empty-title">任务不存在</div>
    </div>
  {:else}
    <div class="grid" style="display: grid; grid-template-columns: 1fr 320px; gap: 1.5rem;">
      <div class="space-y-4">
        <div class="card">
          <div class="card-header">
            <h3 class="font-semibold">视频预览</h3>
          </div>
          <div class="card-body">
            <div class="video-player mb-4">
              {#if task.video?.video_url}
                <video 
                  src={task.video.video_url}
                  controls
                  preload="metadata"
                  poster={task.video?.thumbnail_url}
                  on:timeupdate={(e) => currentTime = e.target.currentTime}
                  on:play={() => isPlaying = true}
                  on:pause={() => isPlaying = false}
                >
                  您的浏览器不支持视频播放
                </video>
              {:else}
                <div class="flex items-center justify-center h-full text-muted">
                  暂无视频
                </div>
              {/if}
            </div>
            
            <div class="flex items-center justify-between text-sm text-secondary">
              <div class="flex items-center gap-2">
                <Clock size={14} />
                <span>{formatDuration(currentTime)} / {formatDuration(task.video?.duration)}</span>
              </div>
              <div class="flex gap-2">
                <button class="btn btn-secondary btn-sm">
                  <Download size={14} />
                  下载
                </button>
                <button class="btn btn-secondary btn-sm">
                  <Share2 size={14} />
                  分享
                </button>
              </div>
            </div>
          </div>
        </div>
        
        <div class="card">
          <div class="card-header flex justify-between items-center">
            <h3 class="font-semibold">视频帧（{frames.length}帧）</h3>
            <span class="text-sm text-muted">
              点击帧可以标记违规
            </span>
          </div>
          <div class="card-body">
            {#if frames.length === 0}
              <div class="empty-state" style="padding: 2rem;">
                暂无视频帧
              </div>
            {:else}
              <div class="frame-gallery">
                {#each frames as frame, index}
                  <div 
                    class="frame-item"
                    class:selected={selectedFrame?.frame_index === frame.frame_index}
                    class:flagged={frame.is_flagged}
                    on:click={() => selectFrame(frame)}
                    on:dblclick={() => openFlagModal(index)}
                  >
                    {#if frame.frame_url}
                      <img src={frame.frame_url} alt="Frame {frame.frame_index}" />
                    {:else}
                      <div class="w-full h-full bg-gray-200 flex items-center justify-center text-muted text-xs">
                        帧 {frame.frame_index}
                      </div>
                    {/if}
                    <div class="frame-timestamp">
                      {formatDuration(frame.timestamp)}
                    </div>
                    {#if frame.is_flagged}
                      <div class="absolute top-1 right-1 bg-red-500 rounded-full p-0.5">
                        <AlertTriangle size={12} color="white" />
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
            
            {#if selectedFrame}
              <div class="mt-4 p-4 bg-gray-50 rounded-lg">
                <div class="flex justify-between items-start">
                  <div>
                    <div class="font-medium">帧 #{selectedFrame.frame_index}</div>
                    <div class="text-sm text-muted">时间: {formatDuration(selectedFrame.timestamp)}</div>
                  </div>
                  {#if !selectedFrame.is_flagged}
                    <button 
                      class="btn btn-danger btn-sm"
                      on:click={() => openFlagModal(frames.indexOf(selectedFrame))}
                    >
                      <AlertTriangle size={14} />
                      标记违规
                    </button>
                  {/if}
                </div>
                {#if selectedFrame.details}
                  <div class="mt-2 text-sm text-muted">
                    <pre style="white-space: pre-wrap; font-size: 12px;">
                      {JSON.stringify(selectedFrame.details, null, 2)}
                    </pre>
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        </div>
        
        {#if autoResult}
          <div class="card">
            <div class="card-header">
              <h3 class="font-semibold">机审结果</h3>
            </div>
            <div class="card-body">
              <div class="mb-4">
                <div class="flex justify-between items-center mb-2">
                  <span class="text-sm text-secondary">综合风险分数</span>
                  <span class="font-bold text-lg {autoResult.overall_score > 0.5 ? 'text-danger' : 'text-success'}">
                    {(autoResult.overall_score * 100).toFixed(1)}%
                  </span>
                </div>
                <div class="progress-bar {autoResult.overall_score > 0.7 ? 'danger' : autoResult.overall_score > 0.4 ? 'warning' : 'success'}">
                  <div class="progress" style="width: {autoResult.overall_score * 100}%;"></div>
                </div>
              </div>
              
              <div class="mb-4">
                <div class="text-sm font-medium mb-2">推荐操作</div>
                <span class="badge {autoResult.recommendation === 'approve' ? 'badge-success' : autoResult.recommendation === 'reject' ? 'badge-danger' : 'badge-warning'}">
                  {autoResult.recommendation === 'approve' ? '通过' : autoResult.recommendation === 'reject' ? '拒绝' : '人工审核'}
                </span>
              </div>
              
              {#if autoResult.violation_scores && Object.keys(autoResult.violation_scores).length > 0}
                <div class="mb-4">
                  <div class="text-sm font-medium mb-2">违规类别分数</div>
                  <div class="space-y-2">
                    {#each Object.entries(autoResult.violation_scores) as [category, score]}
                      {#if score > 0}
                        <div>
                          <div class="flex justify-between text-sm mb-1">
                            <span>{getViolationLabel(category)}</span>
                            <span>{(score * 100).toFixed(1)}%</span>
                          </div>
                          <div class="progress-bar {score > 0.5 ? 'danger' : 'warning'}">
                            <div class="progress" style="width: {score * 100}%;"></div>
                          </div>
                        </div>
                      {/if}
                    {/each}
                  </div>
                </div>
              {/if}
              
              {#if autoResult.text_analysis && (autoResult.text_analysis.score > 0 || autoResult.text_analysis.keywords?.length > 0)}
                <div class="mb-4">
                  <div class="text-sm font-medium mb-2">文本分析</div>
                  <div class="p-3 bg-gray-50 rounded text-sm">
                    <div class="mb-2">
                      <span class="text-secondary">风险分数:</span>
                      <span class="ml-2 font-medium">{(autoResult.text_analysis.score * 100).toFixed(1)}%</span>
                    </div>
                    {#if autoResult.text_analysis.keywords?.length > 0}
                      <div>
                        <span class="text-secondary">敏感关键词:</span>
                        <div class="flex flex-wrap gap-1 mt-1">
                          {#each autoResult.text_analysis.keywords as keyword}
                            <span class="badge badge-danger">{keyword}</span>
                          {/each}
                        </div>
                      </div>
                    {/if}
                  </div>
                </div>
              {/if}
              
              {#if autoResult.flagged_frames?.length > 0}
                <div>
                  <div class="text-sm font-medium mb-2">机审标记帧 ({autoResult.flagged_frames.length})</div>
                  <div class="flex flex-wrap gap-2">
                    {#each autoResult.flagged_frames as frame}
                      <div class="badge badge-danger">
                        帧 {frame.frame_index} ({formatDuration(frame.timestamp)})
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}
            </div>
          </div>
        {/if}
        
        {#if logs.length > 0}
          <div class="card">
            <div class="card-header">
              <h3 class="font-semibold">审核日志</h3>
            </div>
            <div class="card-body">
              <div class="space-y-3">
                {#each logs as log}
                  <div class="flex gap-3">
                    <div class="w-2 h-2 rounded-full mt-2 bg-gray-300 flex-shrink-0"></div>
                    <div class="flex-1">
                      <div class="flex justify-between items-start">
                        <div>
                          <span class="font-medium text-sm">{getActionLabel(log.action)}</span>
                          {#if log.from_status && log.to_status}
                            <span class="text-xs text-muted ml-2">
                              {getStatusBadge(log.from_status).label} → {getStatusBadge(log.to_status).label}
                            </span>
                          {/if}
                        </div>
                        <span class="text-xs text-muted">{formatTime(log.created_at)}</span>
                      </div>
                      {#if log.comment}
                        <div class="text-sm text-secondary mt-1">{log.comment}</div>
                      {/if}
                      {#if log.actor}
                        <div class="text-xs text-muted mt-1">
                          操作人: {log.actor.username} ({getRoleLabel(log.actor.role)})
                        </div>
                      {/if}
                    </div>
                  </div>
                {/each}
              </div>
            </div>
          </div>
        {/if}
      </div>
      
      <div class="space-y-4">
        <div class="card">
          <div class="card-header">
            <h3 class="font-semibold">视频信息</h3>
          </div>
          <div class="card-body">
            <div class="space-y-3">
              <div>
                <div class="text-sm text-secondary mb-1">标题</div>
                <div class="font-medium">{task.video?.title || '无标题'}</div>
              </div>
              {#if task.video?.description}
                <div>
                  <div class="text-sm text-secondary mb-1">描述</div>
                  <div class="text-sm">{task.video.description}</div>
                </div>
              {/if}
              <div>
                <div class="text-sm text-secondary mb-1">时长</div>
                <div>{formatDuration(task.video?.duration)}</div>
              </div>
              <div>
                <div class="text-sm text-secondary mb-1">创建时间</div>
                <div>{formatTime(task.video?.created_at)}</div>
              </div>
              {#if task.sla_deadline}
                <div>
                  <div class="text-sm text-secondary mb-1">SLA 截止时间</div>
                  <div class={dayjs(task.sla_deadline).diff(dayjs(), 'hour') < 6 ? 'text-danger font-medium' : ''}>
                    {formatTime(task.sla_deadline)}
                  </div>
                </div>
              {/if}
            </div>
          </div>
        </div>
        
        {#if canReview}
          <div class="card">
            <div class="card-header">
              <h3 class="font-semibold">审核操作</h3>
            </div>
            <div class="card-body">
              <div class="form-group">
                <label class="form-label">
                  <Tag size={14} class="inline mr-1" />
                  违规标签（拒绝时必填）
                </label>
                <div class="flex flex-wrap gap-2">
                  {#each violationTags as tag}
                    <button 
                      class="px-3 py-1.5 text-sm rounded-full border cursor-pointer transition-colors"
                      class:bg-red-50={selectedViolationTags.includes(tag.name)}
                      class:border-red-300={selectedViolationTags.includes(tag.name)}
                      class:text-red-700={selectedViolationTags.includes(tag.name)}
                      on:click={() => toggleViolationTag(tag.name)}
                      title={tag.description}
                    >
                      {tag.name}
                    </button>
                  {/each}
                </div>
                {#if selectedViolationTags.length > 0}
                  <div class="flex flex-wrap gap-1 mt-2">
                    {#each selectedViolationTags as tag}
                      <span class="badge badge-danger">{tag}</span>
                    {/each}
                  </div>
                {/if}
              </div>
              
              <div class="form-group">
                <label class="form-label">备注</label>
                <textarea 
                  class="form-control" 
                  bind:value={reviewComment}
                  placeholder="请输入审核备注（可选）..."
                  rows={3}
                />
              </div>
              
              <div class="flex gap-2">
                <button 
                  class="btn btn-success flex-1"
                  on:click={() => submitReview('approve')}
                  disabled={isSubmitting}
                >
                  <Check size={16} />
                  通过
                </button>
                <button 
                  class="btn btn-danger flex-1"
                  on:click={() => submitReview('reject')}
                  disabled={isSubmitting || selectedViolationTags.length === 0}
                >
                  <X size={16} />
                  拒绝
                </button>
              </div>
            </div>
          </div>
        {/if}
        
        {#if isFinalState}
          <div class="card">
            <div class="card-header">
              <h3 class="font-semibold">审核结果</h3>
            </div>
            <div class="card-body">
              <div class="text-center p-4">
                {#if humanResult}
                  <div class="mb-4">
                    <span class="badge {humanResult.decision === 'approve' ? 'badge-success text-xl px-4 py-2' : 'badge-danger text-xl px-4 py-2'}">
                      {humanResult.decision === 'approve' ? '通过' : '拒绝'}
                    </span>
                  </div>
                  {#if humanResult.violation_tags?.length > 0}
                    <div class="mb-4">
                      <div class="text-sm text-secondary mb-2">违规标签</div>
                      <div class="flex flex-wrap gap-1 justify-center">
                        {#each humanResult.violation_tags as tag}
                          <span class="badge badge-danger">{tag}</span>
                        {/each}
                      </div>
                    </div>
                  {/if}
                  {#if humanResult.comment}
                    <div class="text-sm text-secondary">
                      备注: {humanResult.comment}
                    </div>
                  {/if}
                {:else}
                  <div class="text-secondary">暂无人工审核记录</div>
                {/if}
              </div>
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>

{#if showFlagModal}
  <div class="modal-overlay" on:click={() => showFlagModal = false}>
    <div class="modal" on:click|stopPropagation>
      <div class="modal-header">
        <h3 class="modal-title">标记违规帧</h3>
        <button class="modal-close" on:click={() => showFlagModal = false}>&times;</button>
      </div>
      <div class="modal-body">
        <p class="mb-4">
          确定要将帧 #{flagFrameIndex} 标记为违规吗？
        </p>
        <div class="form-group">
          <label class="form-label">备注（可选）</label>
          <textarea 
            class="form-control" 
            bind:value={flagComment}
            placeholder="请输入标记原因..."
            rows={2}
          />
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn btn-secondary" on:click={() => showFlagModal = false}>
          取消
        </button>
        <button class="btn btn-danger" on:click={flagFrame}>
          确认标记
        </button>
      </div>
    </div>
  </div>
{/if}

<script context="module">
  function getViolationLabel(category) {
    const labelMap = {
      violence: '暴力血腥',
      nudity: '色情低俗',
      hate_speech: '仇恨言论',
      misinformation: '虚假信息',
      sensitive: '敏感内容',
      gambling: '赌博相关',
      drugs: '毒品相关',
      politics: '政治敏感',
      fraud: '诈骗虚假',
    }
    return labelMap[category] || category
  }

  function getActionLabel(action) {
    const labelMap = {
      start_auto_moderation: '开始机审',
      auto_approve: '机审通过',
      auto_reject: '机审拒绝',
      need_review: '进入人工审核',
      assign_reviewer: '分配审核员',
      start_review: '开始审核',
      human_approve: '人工通过',
      human_reject: '人工拒绝',
      publish: '发布',
      ban: '封禁',
      submit_appeal: '提交申诉',
      approve_appeal: '申诉通过',
      reject_appeal: '申诉驳回',
      reassign_review: '重新审核',
    }
    return labelMap[action] || action
  }

  function getRoleLabel(role) {
    const roleMap = {
      admin: '管理员',
      senior_reviewer: '高级审核员',
      reviewer: '审核员',
    }
    return roleMap[role] || role
  }
</script>

<style>
  .grid {
    display: grid;
  }
  .space-y-4 > * + * {
    margin-top: 1rem;
  }
  .space-y-3 > * + * {
    margin-top: 0.75rem;
  }
  .space-y-2 > * + * {
    margin-top: 0.5rem;
  }
  .gap-4 {
    gap: 1rem;
  }
  .gap-3 {
    gap: 0.75rem;
  }
  .gap-2 {
    gap: 0.5rem;
  }
  .gap-1 {
    gap: 0.25rem;
  }
  .flex-1 {
    flex: 1;
  }
  .flex-shrink-0 {
    flex-shrink: 0;
  }
  .w-full {
    width: 100%;
  }
  .h-full {
    height: 100%;
  }
  .w-2 {
    width: 0.5rem;
  }
  .h-2 {
    height: 0.5rem;
  }
  .text-lg {
    font-size: 1.125rem;
  }
  .text-xl {
    font-size: 1.25rem;
  }
  .mt-4 {
    margin-top: 1rem;
  }
  .mt-2 {
    margin-top: 0.5rem;
  }
  .mt-1 {
    margin-top: 0.25rem;
  }
  .mb-4 {
    margin-bottom: 1rem;
  }
  .mb-2 {
    margin-bottom: 0.5rem;
  }
  .mb-1 {
    margin-bottom: 0.25rem;
  }
  .ml-2 {
    margin-left: 0.5rem;
  }
  .inline {
    display: inline;
  }
  .rounded-full {
    border-radius: 9999px;
  }
  .bg-gray-300 {
    background-color: #d1d5db;
  }
  .bg-gray-50 {
    background-color: #f9fafb;
  }
  .bg-red-50 {
    background-color: #fef2f2;
  }
  .bg-red-500 {
    background-color: #ef4444;
  }
  .border-red-300 {
    border-color: #fca5a5;
  }
  .text-red-700 {
    color: #b91c1c;
  }
  .text-danger {
    color: var(--danger-color);
  }
  .text-success {
    color: var(--success-color);
  }
  .text-muted {
    color: var(--text-muted);
  }
  .text-secondary {
    color: var(--text-secondary);
  }
  .p-4 {
    padding: 1rem;
  }
  .p-3 {
    padding: 0.75rem;
  }
  .p-2 {
    padding: 0.5rem;
  }
  .p-1.5 {
    padding: 0.375rem;
  }
  .px-4 {
    padding-left: 1rem;
    padding-right: 1rem;
  }
  .px-3 {
    padding-left: 0.75rem;
    padding-right: 0.75rem;
  }
  .py-2 {
    padding-top: 0.5rem;
    padding-bottom: 0.5rem;
  }
  .py-1.5 {
    padding-top: 0.375rem;
    padding-bottom: 0.375rem;
  }
  .p-0.5 {
    padding: 0.125rem;
  }
  .absolute {
    position: absolute;
  }
  .relative {
    position: relative;
  }
  .top-1 {
    top: 0.25rem;
  }
  .right-1 {
    right: 0.25rem;
  }
  .justify-center {
    justify-content: center;
  }
  .justify-between {
    justify-content: space-between;
  }
  .items-center {
    align-items: center;
  }
  .items-start {
    align-items: flex-start;
  }
  .transition-colors {
    transition: color, background-color, border-color, text-decoration-color, fill, stroke;
    transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1);
    transition-duration: 150ms;
  }
  .cursor-pointer {
    cursor: pointer;
  }
  .border {
    border-width: 1px;
  }
  .text-xs {
    font-size: 0.75rem;
  }
  .text-sm {
    font-size: 0.875rem;
  }
</style>
