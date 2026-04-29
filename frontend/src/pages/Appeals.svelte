<script>
  import { onMount } from 'svelte'
  import { appealAPI, tagsAPI } from '$lib/api'
  import { showNotification, isSeniorReviewer } from '$lib/store'
  import { 
    AlertTriangle, 
    Check, 
    X, 
    Clock, 
    MessageSquare,
    User,
    Video,
    RefreshCw,
    FileText
  } from 'lucide-svelte'
  import dayjs from 'dayjs'

  let pendingAppeals = []
  let myAppeals = []
  let loading = true
  let violationTags = []
  let activeTab = 'pending'
  
  let showResolveModal = false
  let selectedAppeal = null
  let resolveResult = ''
  let resolveComment = ''

  onMount(async () => {
    try {
      const tagsRes = await tagsAPI.getAll()
      violationTags = tagsRes.data
    } catch (error) {
      console.error('Failed to load tags:', error)
    }
    await loadAppeals()
  })

  async function loadAppeals() {
    loading = true
    try {
      if ($isSeniorReviewer) {
        const pendingRes = await appealAPI.getPending()
        pendingAppeals = pendingRes.data
      }
      
      const myRes = await appealAPI.getMyAppeals({ page_size: 50 })
      myAppeals = myRes.data.appeals || []
    } catch (error) {
      showNotification('加载申诉列表失败', 'error')
    } finally {
      loading = false
    }
  }

  function openResolveModal(appeal) {
    selectedAppeal = appeal
    resolveResult = ''
    resolveComment = ''
    showResolveModal = true
  }

  async function resolveAppeal() {
    if (!resolveResult) {
      showNotification('请选择处理结果', 'warning')
      return
    }

    try {
      await appealAPI.resolve(selectedAppeal.id, {
        result: resolveResult,
        comment: resolveComment,
      })
      
      showNotification(resolveResult === 'approve' ? '申诉已通过' : '申诉已驳回', 'success')
      showResolveModal = false
      await loadAppeals()
    } catch (error) {
      showNotification('处理申诉失败', 'error')
    }
  }

  function getStatusBadge(status) {
    const statusMap = {
      appealed: { class: 'badge-warning', label: '待处理' },
      in_review: { class: 'badge-info', label: '审核中' },
      appeal_approved: { class: 'badge-success', label: '申诉通过' },
      appeal_rejected: { class: 'badge-danger', label: '申诉驳回' },
    }
    return statusMap[status] || { class: 'badge-secondary', label: status }
  }

  function getOriginalDecisionBadge(decision) {
    const map = {
      auto_rejected: { class: 'badge-danger', label: '机审拒绝' },
      human_rejected: { class: 'badge-danger', label: '人工拒绝' },
      banned: { class: 'badge-danger', label: '已封禁' },
    }
    return map[decision] || { class: 'badge-secondary', label: decision }
  }

  function formatTime(timestamp) {
    if (!timestamp) return '-'
    return dayjs(timestamp).format('YYYY-MM-DD HH:mm')
  }
</script>

<div class="page-header">
  <div class="flex justify-between items-center">
    <div>
      <h1 class="page-title">申诉处理</h1>
      <p class="text-secondary text-sm mt-1">
        {#if $isSeniorReviewer}
          待处理: {pendingAppeals.length}
        {/if}
      </p>
    </div>
    <button class="btn btn-secondary" on:click={loadAppeals}>
      <RefreshCw size={16} />
      刷新
    </button>
  </div>
</div>

<div class="page-content">
  {#if loading}
    <div class="loading-state">
      <div class="spinner"></div>
    </div>
  {:else}
    {#if $isSeniorReviewer}
      <div class="mb-6">
        <div class="flex gap-2 mb-4">
          <button 
            class="btn {activeTab === 'pending' ? 'btn-primary' : 'btn-secondary'}"
            on:click={() => activeTab = 'pending'}
          >
            待处理申诉 ({pendingAppeals.length})
          </button>
          <button 
            class="btn {activeTab === 'my' ? 'btn-primary' : 'btn-secondary'}"
            on:click={() => activeTab = 'my'}
          >
            我的申诉 ({myAppeals.length})
          </button>
        </div>
        
        {#if activeTab === 'pending'}
          <div class="card">
            <div class="card-header">
              <h3 class="font-semibold">待处理申诉</h3>
            </div>
            <div class="card-body p-0">
              {#if pendingAppeals.length === 0}
                <div class="empty-state">
                  暂无待处理申诉
                </div>
              {:else}
                <table class="table">
                  <thead>
                    <tr>
                      <th>视频</th>
                      <th>原决定</th>
                      <th>申诉原因</th>
                      <th>提交人</th>
                      <th>提交时间</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each pendingAppeals as appeal}
                      <tr>
                        <td>
                          <div class="flex items-center gap-2">
                            {#if appeal.video?.thumbnail_url}
                              <img 
                                src={appeal.video.thumbnail_url} 
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
                                {appeal.video?.title || '未知视频'}
                              </div>
                              <div class="text-xs text-muted">
                                ID: {appeal.id.slice(0, 8)}...
                              </div>
                            </div>
                          </div>
                        </td>
                        <td>
                          <span class="badge {getOriginalDecisionBadge(appeal.original_decision).class}">
                            {getOriginalDecisionBadge(appeal.original_decision).label}
                          </span>
                        </td>
                        <td>
                          <div class="text-sm max-w-xs truncate" title={appeal.reason}>
                            {appeal.reason}
                          </div>
                        </td>
                        <td>
                          <div class="flex items-center gap-1">
                            <User size={14} class="text-muted" />
                            <span class="text-sm">{appeal.submitter?.username || '未知'}</span>
                          </div>
                        </td>
                        <td>
                          <span class="text-sm">{formatTime(appeal.created_at)}</span>
                        </td>
                        <td>
                          <button 
                            class="btn btn-primary btn-sm"
                            on:click={() => openResolveModal(appeal)}
                          >
                            <MessageSquare size={14} />
                            处理
                          </button>
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              {/if}
            </div>
          </div>
        {/if}
        
        {#if activeTab === 'my'}
          <div class="card">
            <div class="card-header">
              <h3 class="font-semibold">我的申诉</h3>
            </div>
            <div class="card-body p-0">
              {#if myAppeals.length === 0}
                <div class="empty-state">
                  暂无申诉记录
                </div>
              {:else}
                <table class="table">
                  <thead>
                    <tr>
                      <th>视频</th>
                      <th>原决定</th>
                      <th>申诉原因</th>
                      <th>状态</th>
                      <th>处理结果</th>
                      <th>提交时间</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each myAppeals as appeal}
                      <tr>
                        <td>
                          <div class="text-sm font-medium">
                            {appeal.video?.title || '未知视频'}
                          </div>
                          <div class="text-xs text-muted">
                            ID: {appeal.id.slice(0, 8)}...
                          </div>
                        </td>
                        <td>
                          <span class="badge {getOriginalDecisionBadge(appeal.original_decision).class}">
                            {getOriginalDecisionBadge(appeal.original_decision).label}
                          </span>
                        </td>
                        <td>
                          <div class="text-sm max-w-xs truncate" title={appeal.reason}>
                            {appeal.reason}
                          </div>
                        </td>
                        <td>
                          <span class="badge {getStatusBadge(appeal.status).class}">
                            {getStatusBadge(appeal.status).label}
                          </span>
                        </td>
                        <td>
                          {#if appeal.appeal_result}
                            <div class="text-sm">
                              {appeal.appeal_result === 'approve' ? '通过' : '驳回'}
                            </div>
                            {#if appeal.appeal_comment}
                              <div class="text-xs text-muted mt-0.5">
                                {appeal.appeal_comment}
                              </div>
                            {/if}
                          {:else}
                            <span class="text-muted text-sm">-</span>
                          {/if}
                        </td>
                        <td>
                          <span class="text-sm">{formatTime(appeal.created_at)}</span>
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              {/if}
            </div>
          </div>
        {/if}
      </div>
    {:else}
      <div class="card">
        <div class="card-header">
          <h3 class="font-semibold">我的申诉</h3>
        </div>
        <div class="card-body p-0">
          {#if myAppeals.length === 0}
            <div class="empty-state">
              <FileText size={48} class="empty-icon" />
              <div class="empty-title">暂无申诉记录</div>
              <div class="text-sm text-muted">如果您对审核结果有异议，可以提交申诉</div>
            </div>
          {:else}
            <table class="table">
              <thead>
                <tr>
                  <th>视频</th>
                  <th>原决定</th>
                  <th>申诉原因</th>
                  <th>状态</th>
                  <th>处理结果</th>
                  <th>提交时间</th>
                </tr>
              </thead>
              <tbody>
                {#each myAppeals as appeal}
                  <tr>
                    <td>
                      <div class="text-sm font-medium">
                        {appeal.video?.title || '未知视频'}
                      </div>
                      <div class="text-xs text-muted">
                        ID: {appeal.id.slice(0, 8)}...
                      </div>
                    </td>
                    <td>
                      <span class="badge {getOriginalDecisionBadge(appeal.original_decision).class}">
                        {getOriginalDecisionBadge(appeal.original_decision).label}
                      </span>
                    </td>
                    <td>
                      <div class="text-sm max-w-xs truncate" title={appeal.reason}>
                        {appeal.reason}
                      </div>
                    </td>
                    <td>
                      <span class="badge {getStatusBadge(appeal.status).class}">
                        {getStatusBadge(appeal.status).label}
                      </span>
                    </td>
                    <td>
                      {#if appeal.appeal_result}
                        <div class="text-sm">
                          {appeal.appeal_result === 'approve' ? '通过' : '驳回'}
                        </div>
                        {#if appeal.appeal_comment}
                          <div class="text-xs text-muted mt-0.5">
                            {appeal.appeal_comment}
                          </div>
                        {/if}
                      {:else}
                        <span class="text-muted text-sm">处理中</span>
                      {/if}
                    </td>
                    <td>
                      <span class="text-sm">{formatTime(appeal.created_at)}</span>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </div>
      </div>
    {/if}
  {/if}
</div>

{#if showResolveModal && selectedAppeal}
  <div class="modal-overlay" on:click={() => showResolveModal = false}>
    <div class="modal" on:click|stopPropagation>
      <div class="modal-header">
        <h3 class="modal-title">处理申诉</h3>
        <button class="modal-close" on:click={() => showResolveModal = false}>&times;</button>
      </div>
      <div class="modal-body">
        <div class="mb-4 p-4 bg-gray-50 rounded-lg">
          <div class="mb-2">
            <span class="text-sm text-secondary">视频:</span>
            <span class="ml-2 font-medium">{selectedAppeal.video?.title || '未知视频'}</span>
          </div>
          <div class="mb-2">
            <span class="text-sm text-secondary">原决定:</span>
            <span class="ml-2 badge {getOriginalDecisionBadge(selectedAppeal.original_decision).class}">
              {getOriginalDecisionBadge(selectedAppeal.original_decision).label}
            </span>
          </div>
          <div>
            <span class="text-sm text-secondary">申诉原因:</span>
            <div class="mt-1 text-sm">{selectedAppeal.reason}</div>
          </div>
        </div>
        
        <div class="form-group">
          <label class="form-label">处理结果</label>
          <div class="flex gap-4">
            <label class="flex items-center gap-2 cursor-pointer">
              <input 
                type="radio" 
                bind:group={resolveResult} 
                value="approve"
              />
              <span class="text-success font-medium">通过申诉</span>
            </label>
            <label class="flex items-center gap-2 cursor-pointer">
              <input 
                type="radio" 
                bind:group={resolveResult} 
                value="reject"
              />
              <span class="text-danger font-medium">驳回申诉</span>
            </label>
          </div>
        </div>
        
        <div class="form-group">
          <label class="form-label">处理备注</label>
          <textarea 
            class="form-control" 
            bind:value={resolveComment}
            placeholder="请输入处理备注..."
            rows={3}
          />
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn btn-secondary" on:click={() => showResolveModal = false}>
          取消
        </button>
        <button 
          class="btn btn-primary"
          on:click={resolveAppeal}
          disabled={!resolveResult}
        >
          确认处理
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .gap-2 {
    gap: 0.5rem;
  }
  .gap-4 {
    gap: 1rem;
  }
  .flex-1 {
    flex: 1;
  }
  .w-12 {
    width: 3rem;
  }
  .h-8 {
    height: 2rem;
  }
  .object-cover {
    object-fit: cover;
  }
  .rounded {
    border-radius: var(--radius-md);
  }
  .bg-gray-100 {
    background-color: #f3f4f6;
  }
  .bg-gray-50 {
    background-color: #f9fafb;
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
  .p-0 {
    padding: 0;
  }
  .mt-6 {
    margin-top: 1.5rem;
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
  .mt-0\.5 {
    margin-top: 0.125rem;
  }
  .mb-6 {
    margin-bottom: 1.5rem;
  }
  .mb-4 {
    margin-bottom: 1rem;
  }
  .mb-2 {
    margin-bottom: 0.5rem;
  }
  .ml-2 {
    margin-left: 0.5rem;
  }
  .max-w-xs {
    max-width: 20rem;
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
</style>
