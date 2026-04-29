<script>
  import { Link, navigate } from 'svelte-routing'
  import { user, isAuthenticated, logout, isSeniorReviewer } from '$lib/store'
  import { 
    Home, 
    ListTodo, 
    ClipboardList, 
    AlertTriangle, 
    BarChart3, 
    LogOut,
    User
  } from 'lucide-svelte'
  import Notification from './Notification.svelte'

  export let children
  
  let currentPath = window.location.pathname

  function handleLogout() {
    logout()
    navigate('/login')
  }
</script>

<Notification />

<div class="sidebar">
  <div class="sidebar-header">
    <div class="sidebar-title">内容审核平台</div>
    <div class="text-xs text-muted mt-1">Content Moderation</div>
  </div>
  
  <nav class="sidebar-nav">
    <Link to="/" class="sidebar-nav-item" class:active={currentPath === '/' || currentPath === ''}>
      <Home size={18} />
      <span>仪表盘</span>
    </Link>
    
    <Link to="/tasks" class="sidebar-nav-item" class:active={currentPath.startsWith('/tasks') && currentPath !== '/my-tasks'}>
      <ListTodo size={18} />
      <span>任务列表</span>
    </Link>
    
    <Link to="/my-tasks" class="sidebar-nav-item" class:active={currentPath === '/my-tasks'}>
      <ClipboardList size={18} />
      <span>我的任务</span>
    </Link>
    
    <Link to="/appeals" class="sidebar-nav-item" class:active={currentPath === '/appeals'}>
      <AlertTriangle size={18} />
      <span>申诉处理</span>
    </Link>
    
    {#if $isSeniorReviewer}
      <Link to="/statistics" class="sidebar-nav-item" class:active={currentPath === '/statistics'}>
        <BarChart3 size={18} />
        <span>统计报表</span>
      </Link>
    {/if}
  </nav>
  
  <div style="margin-top: auto; padding: 0.5rem; border-top: 1px solid var(--border-color);">
    <div class="flex items-center gap-2 mb-2" style="padding: 0.625rem 0.875rem;">
      <User size={18} class="text-secondary" />
      <div>
        <div class="text-sm font-semibold">{$user?.username}</div>
        <div class="text-xs text-muted">{getRoleLabel($user?.role)}</div>
      </div>
    </div>
    <button class="btn btn-secondary w-full" on:click={handleLogout}>
      <LogOut size={16} />
      退出登录
    </button>
  </div>
</div>

<div class="main-content">
  <slot>
    {@html children}
  </slot>
</div>

<script context="module">
  function getRoleLabel(role) {
    const roleMap = {
      admin: '管理员',
      senior_reviewer: '高级审核员',
      reviewer: '审核员',
    }
    return roleMap[role] || role
  }
</script>
