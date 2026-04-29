<script>
  import { onMount } from 'svelte'
  import { taskAPI, tagsAPI } from '$lib/api'
  import { showNotification, isSeniorReviewer } from '$lib/store'
  import { navigate } from 'svelte-routing'
  import { 
    Search, 
    Filter, 
    RefreshCw, 
    Play, 
    Check, 
    X, 
    Clock,
    User,
    AlertTriangle
  } from 'lucide-svelte'
  import dayjs from 'dayjs'

  let tasks = []
  let loading = true
  let currentPage = 1
  let pageSize = 20
  let total = 0
  let totalPages = 1
  
  let filters = {
    status: '',
    priority: '',
    assigned_to: '',
  }
  
  let selectedTasks = new Set()
  let violationTags = []
  let showBatchModal = false
  let batchOperation = ''
  let batchRejectReason = ''
  let batchViolationTags = []
  
  let searchKeyword = ''

  onMount(async () => {
    try {
      const tagsRes = await tagsAPI.getAll()
      violationTags = tagsRes.data
    } catch (error) {
      console.error('Failed to load tags:', error)
    }
    await loadTasks()
  })

  async function loadTasks() {
    loading = true
    try {
      const params = {
        page: currentPage,
        page_size: pageSize,
        ...filters,
      }
      
      if (searchKeyword) {
        params.keyword = searchKeyword
      }
      
      const response = await taskAPI.getTasks(params)
      tasks = response.data.tasks
      total = response.data.total
      totalPages = response.data.total_pages
    } catch (error) {
      showNotification('加载任务列表失败', 'error')
    } finally {
      loading = false
    }
  }

  async function handleFilter() {
    currentPage = 1
    await loadTasks()
  }

  async function handleRefresh() {
    await loadTasks()
    showNotification('已刷新', 'success')
  }

  function toggleTaskSelection(taskId) {
    if (selectedTasks.has(taskId)) {
      selectedTasks.delete(taskId)
    } else {
      selectedTasks.add(taskId)
    }
  }

  function toggleSelectAll() {
    if (selectedTasks.size === tasks.length) {
      selectedTasks.clear()
    } else {
      tasks.forEach(task => selectedTasks.add(task.id))
    }
  }

  function openBatchModal(operation) {
    if (selectedTasks.size === 0) {
      showNotification('请先选择任务', 'warning')
      return
    }
    batchOperation = operation
    batchRejectReason = ''
    batchViolationTags = []
    showBatchModal = true
  }

  async function executeBatchOperation() {
    showBatchModal = false
    
    try {
      const taskIds = Array.from(selectedTasks)
      let params = {}
      
      if (batchOperation === 'reject') {
        params = {
          violation_tags: batchViolationTags,
          comment: batchRejectReason,
        }
      }
      
      showNotification('批量操作已开始，请稍后查看结果', 'info')
      
      setTimeout(async () => {
        for (const taskId of taskIds) {
          try {
            if (batchOperation === 'approve') {
              await taskAPI.submitReview(taskId, {
                decision: 'approve',
                violation_tags: [],
                comment: '批量通过',
              })
            } else if (batchOperation === 'reject') {
              await taskAPI.submitReview(taskId, {
                decision: 'reject',
                violation_tags: batchViolationTags,
                comment: batchRejectReason,
              })
            }
          } catch (error) {
            console.error(`Failed to process task ${taskId}:`, error)
          }
        }
        
        selectedTasks.clear()
        await loadTasks()
        showNotification('批量操作完成', 'success')
      }, 100)
      
    } catch (error) {
      showNotification('批量操作失败', 'error')
    }
  }

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
    return dayjs(timestamp).format('MM-DD HH:mm')
  }

  function isSLAAtRisk(task) {
    if (!task.sla_deadline) return false
    const deadline = dayjs(task.sla_deadline)
    const now = dayjs()
    const hoursLeft = deadline.diff(now, 'hour')
    return hoursLeft < 6
  }

  function goToReview(task) {
    navigate(`/tasks/${task.id}`)
  }

  $: isAllSelected = tasks.length > 0 && selectedTasks.size === tasks.length
  $: isSomeSelected = selectedTasks.size > 0 && selectedTasks.size < tasks.length
</script>

<div class="page-header">
  <div class="flex justify-between items-center">
    <div>
      <h1 class="page-title">任务列表</h1>
      <p class="text-secondary text-sm mt-1">共 {total} 条任务</p>
    </div>
    <div class="flex gap-2">
      <button class="btn btn-secondary" on:click={handleRefresh}>
        <RefreshCw size={16} />
        刷新
      </button>
      {#if $isSeniorReviewer}
        <button 
          class="btn btn-success" 
          on:click={() => openBatchModal('approve')}
          disabled={selectedTasks.size === 0}
        >
          <Check size={16} />
          批量通过 ({selectedTasks.size})
        </button>
        <button 
          class="btn btn-danger" 
          on:click={() => openBatchModal('reject')}
          disabled={selectedTasks.size === 0}
        >
          <X size={16} />
          批量拒绝 ({selectedTasks.size})
        </button>
      {/if}
    </div>
  </div>
</div>

<div class="page-content">
  <div class="card mb-4">
    <div class="card-body">
      <div class="flex gap-4 items-end flex-wrap">
        <div class="form-group mb-0" style="min-width: 200px;">
          <label class="form-label">搜索</label>
          <div class="relative">
            <input 
              type="text" 
              class="form-control" 
              bind:value={searchKeyword}
              placeholder="搜索视频标题..."
              on:keydown={(e) => e.key === 'Enter' && handleFilter()}
            />
            <Search size={16} class="absolute right-3 top-1/2 -translate-y-1/2 text-muted" />
          </div>
        </div>
        
        <div class="form-group mb-0">
          <label class="form-label">状态</label>
          <select class="form-control" bind:value={filters.status} on:change={handleFilter}>
            <option value="">全部</option>
            <option value="pending">待处理</option>
            <option value="auto_moderating">机审中</option>
            <option value="need_review">待人工审核</option>
            <option value="assigned">已分配</option>
            <option value="in_review">审核中</option>
            <option value="human_approved">人工通过</option>
            <option value="human_rejected">人工拒绝</option>
            <option value="published">已发布</option>
            <option value="banned">已封禁</option>
          </select>
        </div>
        
        <div class="form-group mb-0">
          <label class="form-label">优先级</label>
          <select class="form-control" bind:value={filters.priority} on:change={handleFilter}>
            <option value="">全部</option>
            <option value="high">高</option>
            <option value="normal">正常</option>
            <option value="low">低</option>
          </select>
        </div>
        
        <button class="btn btn-primary" on:click={handleFilter}>
          <Filter size={16} />
          筛选
        </button>
      </div>
    </div>
  </div>

  <div class="card">
    {#if loading}
      <div class="loading-state">
        <div class="spinner"></div>
      </div>
    {:else if tasks.length === 0}
      <div class="empty-state">
        <Search size={48} class="empty-icon" />
        <div class="empty-title">暂无任务</div>
        <div class="text-sm text-muted">没有找到符合条件的审核任务</div>
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="table">
          <thead>
            <tr>
              {#if $isSeniorReviewer}
                <th style="width: 40px;">
                  <input 
                    type="checkbox" 
                    checked={isAllSelected}
                    indeterminate={isSomeSelected}
                    on:change={toggleSelectAll}
                  />
                </th>
              {/if}
              <th>视频</th>
              <th>状态</th>
              <th>优先级</th>
              <th>分配给</th>
              <th>SLA</th>
              <th>创建时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {#each tasks as task}
              <tr class:bg-yellow-50={isSLAAtRisk(task)}>
                {#if $isSeniorReviewer}
                  <td>
                    <input 
                      type="checkbox" 
                      checked={selectedTasks.has(task.id)}
                      on:change={() => toggleTaskSelection(task.id)}
                    />
                  </td>
                {/if}
                <td>
                  <div class="flex items-center gap-3">
                    {#if task.video?.thumbnail_url}
                      <img 
                        src={task.video.thumbnail_url} 
                        alt=""
                        class="w-16 h-10 object-cover rounded"
                      />
                    {:else}
                      <div class="w-16 h-10 bg-gray-200 rounded flex items-center justify-center">
                        <Video size={20} class="text-muted" />
                      </div>
                    {/if}
                    <div>
                      <div class="font-medium text-sm">{task.video?.title || '未知视频'}</div>
                      <div class="text-xs text-muted">ID: {task.id.slice(0, 8)}</div>
                    </div>
                  </div>
                </td>
                <td>
                  <span class="badge {getStatusBadge(task.current_status).class}">
                    {getStatusBadge(task.current_status).label}
                  </span>
                </td>
                <td>
                  <span class="badge {getPriorityBadge(task.priority).class}">
                    {getPriorityBadge(task.priority).label}
                  </span>
                </td>
                <td>
                  {#if task.assignee}
                    <div class="flex items-center gap-1">
                      <User size={14} class="text-muted" />
                      <span class="text-sm">{task.assignee.username}</span>
                    </div>
                  {:else}
                    <span class="text-muted text-sm">未分配</span>
                  {/if}
                </td>
                <td>
                  {#if isSLAAtRisk(task)}
                    <div class="flex items-center gap-1 text-danger">
                      <AlertTriangle size={14} />
                      <span class="text-sm">{formatTime(task.sla_deadline)}</span>
                    </div>
                  {:else}
                    <span class="text-sm">{formatTime(task.sla_deadline)}</span>
                  {/if}
                </td>
                <td>
                  <span class="text-sm">{formatTime(task.created_at)}</span>
                </td>
                <td>
                  <button 
                    class="btn btn-primary btn-sm"
                    on:click={() => goToReview(task)}
                  >
                    <Play size={14} />
                    审核
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      
      {#if totalPages > 1}
        <div class="card-footer">
          <div class="pagination">
            <button 
              disabled={currentPage === 1}
              on:click={() => { currentPage--; loadTasks(); }}
            >
              上一页
            </button>
            
            {#each Array.from({ length: Math.min(5, totalPages) }, (_, i) => {
              let pageNum;
              if (totalPages <= 5) {
                pageNum = i + 1;
              } else if (currentPage <= 3) {
                pageNum = i + 1;
              } else if (currentPage >= totalPages - 2) {
                pageNum = totalPages - 4 + i;
              } else {
                pageNum = currentPage - 2 + i;
              }
              return pageNum;
            }) as pageNum}
              <button 
                class:active={currentPage === pageNum}
                on:click={() => { currentPage = pageNum; loadTasks(); }}
              >
                {pageNum}
              </button>
            {/each}
            
            <button 
              disabled={currentPage === totalPages}
              on:click={() => { currentPage++; loadTasks(); }}
            >
              下一页
            </button>
          </div>
        </div>
      {/if}
    {/if}
  </div>
</div>

{#if showBatchModal}
  <div class="modal-overlay" on:click={() => showBatchModal = false}>
    <div class="modal" on:click|stopPropagation>
      <div class="modal-header">
        <h3 class="modal-title">
          {batchOperation === 'approve' ? '批量通过' : '批量拒绝'}
        </h3>
        <button class="modal-close" on:click={() => showBatchModal = false}>&times;</button>
      </div>
      <div class="modal-body">
        <p class="mb-4">
          确定要批量{batchOperation === 'approve' ? '通过' : '拒绝'} <strong>{selectedTasks.size}</strong> 个任务吗？
        </p>
        
        {#if batchOperation === 'reject'}
          <div class="form-group">
            <label class="form-label">违规标签</label>
            <div class="checkbox-group">
              {#each violationTags as tag}
                <label class="checkbox-item">
                  <input 
                    type="checkbox" 
                    checked={batchViolationTags.includes(tag.name)}
                    on:change={(e) => {
                      if (e.target.checked) {
                        batchViolationTags = [...batchViolationTags, tag.name]
                      } else {
                        batchViolationTags = batchViolationTags.filter(t => t !== tag.name)
                      }
                    }}
                  />
                  <span>{tag.name}</span>
                </label>
              {/each}
            </div>
          </div>
          
          <div class="form-group">
            <label class="form-label">备注</label>
            <textarea 
              class="form-control" 
              bind:value={batchRejectReason}
              placeholder="请输入拒绝原因..."
            />
          </div>
        {/if}
      </div>
      <div class="modal-footer">
        <button class="btn btn-secondary" on:click={() => showBatchModal = false}>
          取消
        </button>
        <button 
          class="btn {batchOperation === 'approve' ? 'btn-success' : 'btn-danger'}"
          on:click={executeBatchOperation}
        >
          确认执行
        </button>
      </div>
    </div>
  </div>
{/if}

<script context="module">
  import { Video } from 'lucide-svelte'
</script>

<style>
  .relative {
    position: relative;
  }
  .right-3 {
    right: 0.75rem;
  }
  .top-1\/2 {
    top: 50%;
  }
  .-translate-y-1\/2 {
    transform: translateY(-50%);
  }
  .flex-wrap {
    flex-wrap: wrap;
  }
  .w-16 {
    width: 4rem;
  }
  .h-10 {
    height: 2.5rem;
  }
  .object-cover {
    object-fit: cover;
  }
  .rounded {
    border-radius: var(--radius-md);
  }
  .bg-gray-200 {
    background-color: #e5e7eb;
  }
  .bg-yellow-50 {
    background-color: #fefce8;
  }
  .text-danger {
    color: var(--danger-color);
  }
  .overflow-x-auto {
    overflow-x: auto;
  }
  .mb-4 {
    margin-bottom: 1rem;
  }
  .mb-0 {
    margin-bottom: 0;
  }
</style>
