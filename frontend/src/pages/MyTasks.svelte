<script>
  import { onMount } from 'svelte'
  import { taskAPI } from '$lib/api'
  import { showNotification } from '$lib/store'
  import { navigate } from 'svelte-routing'
  import { 
    Play, 
    Clock, 
    CheckCircle, 
    XCircle, 
    AlertTriangle,
    Video,
    User,
    ChevronLeft,
    ChevronRight
  } from 'lucide-svelte'
  import dayjs from 'dayjs'

  let pendingTasks = []
  let historyTasks = []
  let loading = true
  let currentHistoryPage = 1
  let historyTotalPages = 1
  let historyTotal = 0

  onMount(async () => {
    await Promise.all([
      loadPendingTasks(),
      loadHistoryTasks(),
    ])
  })

  async function loadPendingTasks() {
    try {
      const response = await taskAPI.getMyPending()
      pendingTasks = response.data
    } catch (error) {
      showNotification('加载待办任务失败', 'error')
    }
  }

  async function loadHistoryTasks() {
    try {
      const response = await taskAPI.getMyHistory({
        page: currentHistoryPage,
        page_size: 10,
      })
      historyTasks = response.data.results
      historyTotal = response.data.total
      historyTotalPages = response.data.total_pages
    } catch (error) {
      showNotification('加载历史记录失败', 'error')
    } finally {
      loading = false
    }
  }

  function goToReview(task) {
    navigate(`/tasks/${task.id}`)
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
      high: { class: 'badge-danger', label: '高' },
      normal: { class: 'badge-primary', label: '正常' },
      low: { class: 'badge-secondary', label: '低' },
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

  function getRemainingTime(task) {
    if (!task.sla_deadline) return null
    const deadline = dayjs(task.sla_deadline)
    const now = dayjs()
    const diff = deadline.diff(now, 'minute')
    
    if (diff < 0) {
      return { text: '已超时', class: 'text-danger' }
    } else if (diff < 60) {
      return { text: `${diff}分钟`, class: 'text-danger' }
    } else if (diff < 360) {
      return { text: `${Math.floor(diff / 60)}小时`, class: 'text-warning' }
    } else {
      return { text: `${Math.floor(diff / 60)}小时`, class: 'text-secondary' }
    }
  }
</script>

<div class="page-header">
  <h1 class="page-title">我的任务</h1>
  <p class="text-secondary text-sm mt-1">
    待审核: {pendingTasks.length} | 今日已完成: {historyTasks.filter(t => dayjs(t.created_at).isSame(dayjs(), 'day')).length}
  </p>
</div>

<div class="page-content">
  {#if loading}
    <div class="loading-state">
      <div class="spinner"></div>
    </div>
  {:else}
    <div class="card mb-6">
      <div class="card-header">
        <div class="flex justify-between items-center">
          <h3 class="font-semibold">待审核任务</h3>
          <span class="badge {pendingTasks.length > 0 ? 'badge-warning' : 'badge-secondary'}">
            {pendingTasks.length} 个待处理
          </span>
        </div>
      </div>
      <div class="card-body">
        {#if pendingTasks.length === 0}
          <div class="empty-state">
            <CheckCircle size={48} class="empty-icon" />
            <div class="empty-title">暂无待审核任务</div>
            <div class="text-sm text-muted">您的工作已完成，请休息一下</div>
          </div>
        {:else}
          <div class="grid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(400px, 1fr)); gap: 1rem;">
            {#each pendingTasks as task}
              <div class="border rounded-lg p-4 {isSLAAtRisk(task) ? 'border-yellow-300 bg-yellow-50' : 'border-gray-200'}">
                <div class="flex justify-between items-start mb-3">
                  <div class="flex items-center gap-2">
                    <span class="badge {getPriorityBadge(task.priority).class}">
                      {getPriorityBadge(task.priority).label}
                    </span>
                    <span class="badge {getStatusBadge(task.current_status).class}">
                      {getStatusBadge(task.current_status).label}
                    </span>
                  </div>
                  {#if getRemainingTime(task)}
                    <span class="text-sm font-medium {getRemainingTime(task).class}">
                      {getRemainingTime(task).text}
                    </span>
                  {/if}
                </div>
                
                <div class="flex items-center gap-3 mb-3">
                  {#if task.video?.thumbnail_url}
                    <img 
                      src={task.video.thumbnail_url} 
                      alt=""
                      class="w-20 h-12 object-cover rounded"
                    />
                  {:else}
                    <div class="w-20 h-12 bg-gray-100 rounded flex items-center justify-center">
                      <Video size={20} class="text-muted" />
                    </div>
                  {/if}
                  <div class="flex-1 min-w-0">
                    <div class="font-medium text-sm truncate">
                      {task.video?.title || '未知视频'}
                    </div>
                    <div class="text-xs text-muted mt-0.5">
                      ID: {task.id.slice(0, 8)}...
                    </div>
                  </div>
                </div>
                
                {#if task.assignee}
                  <div class="flex items-center gap-1 text-xs text-muted mb-3">
                    <User size={12} />
                    <span>分配给: {task.assignee.username}</span>
                  </div>
                {/if}
                
                <div class="flex justify-between items-center">
                  <span class="text-xs text-muted">
                    <Clock size={12} class="inline mr-1" />
                    {formatTime(task.created_at)}
                  </span>
                  <button 
                    class="btn btn-primary btn-sm"
                    on:click={() => goToReview(task)}
                  >
                    <Play size={14} />
                    开始审核
                  </button>
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>
    
    <div class="card">
      <div class="card-header">
        <div class="flex justify-between items-center">
          <h3 class="font-semibold">审核历史</h3>
          <span class="text-sm text-muted">共 {historyTotal} 条记录</span>
        </div>
      </div>
      <div class="card-body p-0">
        {#if historyTasks.length === 0}
          <div class="empty-state">
            暂无审核历史
          </div>
        {:else}
          <table class="table">
            <thead>
              <tr>
                <th>视频</th>
                <th>状态</th>
                <th>违规标签</th>
                <th>审核时间</th>
              </tr>
            </thead>
            <tbody>
              {#each historyTasks as result}
                <tr>
                  <td>
                    <div class="flex items-center gap-2">
                      {#if result.task?.video?.thumbnail_url}
                        <img 
                          src={result.task.video.thumbnail_url} 
                          alt=""
                          class="w-12 h-8 object-cover rounded"
                        />
                      {:else}
                        <div class="w-12 h-8 bg-gray-100 rounded flex items-center justify-center">
                          <Video size={14} class="text-muted" />
                        </div>
                      {/if}
                      <div>
                        <div class="text-sm font-medium">
                          {result.task?.video?.title || '未知视频'}
                        </div>
                        <div class="text-xs text-muted">
                          ID: {result.task_id?.slice(0, 8) || result.id?.slice(0, 8)}...
                        </div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <span class="badge {result.decision === 'approve' ? 'badge-success' : 'badge-danger'}">
                      {result.decision === 'approve' ? '通过' : '拒绝'}
                    </span>
                  </td>
                  <td>
                    {#if result.violation_tags?.length > 0}
                      <div class="flex flex-wrap gap-1">
                        {#each result.violation_tags.slice(0, 3) as tag}
                          <span class="badge badge-danger">{tag}</span>
                        {/each}
                        {#if result.violation_tags.length > 3}
                          <span class="badge badge-secondary">+{result.violation_tags.length - 3}</span>
                        {/if}
                      </div>
                    {:else}
                      <span class="text-muted text-sm">-</span>
                    {/if}
                  </td>
                  <td>
                    <div class="text-sm">{formatTime(result.created_at)}</div>
                    {#if result.review_duration}
                      <div class="text-xs text-muted">
                        用时: {result.review_duration.toFixed(0)}秒
                      </div>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
          
          {#if historyTotalPages > 1}
            <div class="card-footer">
              <div class="pagination">
                <button 
                  disabled={currentHistoryPage === 1}
                  on:click={() => { currentHistoryPage--; loadHistoryTasks(); }}
                >
                  <ChevronLeft size={16} />
                  上一页
                </button>
                
                <span class="text-sm text-muted mx-4">
                  第 {currentHistoryPage} / {historyTotalPages} 页
                </span>
                
                <button 
                  disabled={currentHistoryPage === historyTotalPages}
                  on:click={() => { currentHistoryPage++; loadHistoryTasks(); }}
                >
                  下一页
                  <ChevronRight size={16} />
                </button>
              </div>
            </div>
          {/if}
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .grid {
    display: grid;
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
  .min-w-0 {
    min-width: 0;
  }
  .w-full {
    width: 100%;
  }
  .w-20 {
    width: 5rem;
  }
  .w-12 {
    width: 3rem;
  }
  .h-12 {
    height: 3rem;
  }
  .h-8 {
    height: 2rem;
  }
  .text-lg {
    font-size: 1.125rem;
  }
  .text-xl {
    font-size: 1.25rem;
  }
  .mt-6 {
    margin-top: 1.5rem;
  }
  .mt-4 {
    margin-top: 1rem;
  }
  .mt-3 {
    margin-top: 0.75rem;
  }
  .mt-2 {
    margin-top: 0.5rem;
  }
  .mt-1 {
    margin-top: 0.25rem;
  }
  .mt-0\.5 {
    margin-top: 0.125rem;
  }
  .mb-6 {
    margin-bottom: 1.5rem;
  }
  .mb-4 {
    margin-bottom: 1rem;
  }
  .mb-3 {
    margin-bottom: 0.75rem;
  }
  .mb-2 {
    margin-bottom: 0.5rem;
  }
  .mx-4 {
    margin-left: 1rem;
    margin-right: 1rem;
  }
  .mr-1 {
    margin-right: 0.25rem;
  }
  .inline {
    display: inline;
  }
  .rounded-lg {
    border-radius: var(--radius-lg);
  }
  .rounded {
    border-radius: var(--radius-md);
  }
  .border {
    border-width: 1px;
  }
  .border-gray-200 {
    border-color: #e5e7eb;
  }
  .border-yellow-300 {
    border-color: #fcd34d;
  }
  .bg-gray-100 {
    background-color: #f3f4f6;
  }
  .bg-yellow-50 {
    background-color: #fefce8;
  }
  .text-danger {
    color: var(--danger-color);
  }
  .text-warning {
    color: var(--warning-color);
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
  .p-0 {
    padding: 0;
  }
  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
</style>
