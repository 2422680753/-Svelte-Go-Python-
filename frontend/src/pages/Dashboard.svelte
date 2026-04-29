<script>
  import { onMount } from 'svelte'
  import { statsAPI } from '$lib/api'
  import { user, isSeniorReviewer } from '$lib/store'
  import { 
    TrendingUp, 
    TrendingDown, 
    Clock, 
    AlertTriangle,
    CheckCircle,
    XCircle,
    Video,
    Users,
    Activity
  } from 'lucide-svelte'
  import { Bar, Doughnut, Line } from 'svelte-chartjs'
  import {
    Chart as ChartJS,
    CategoryScale,
    LinearScale,
    PointElement,
    LineElement,
    BarElement,
    ArcElement,
    Title,
    Tooltip,
    Legend,
    Filler
  } from 'chart.js'
  import dayjs from 'dayjs'

  let loading = true
  let dashboardStats = null
  let trendData = null
  let violationStats = null
  let slaPerformance = null
  
  let lineChartData = null
  let barChartData = null
  let doughnutChartData = null

  ChartJS.register(
    CategoryScale,
    LinearScale,
    PointElement,
    LineElement,
    BarElement,
    ArcElement,
    Title,
    Tooltip,
    Legend,
    Filler
  )

  const lineChartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        display: false,
      },
      tooltip: {
        mode: 'index',
        intersect: false,
      },
    },
    scales: {
      x: {
        grid: {
          display: false,
        },
      },
      y: {
        beginAtZero: true,
      },
    },
    interaction: {
      mode: 'nearest',
      axis: 'x',
      intersect: false,
    },
  }

  const barChartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        display: false,
      },
    },
    scales: {
      y: {
        beginAtZero: true,
      },
    },
  }

  const doughnutChartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        position: 'right',
      },
    },
  }

  onMount(async () => {
    try {
      const [statsRes, trendsRes, slaRes] = await Promise.all([
        statsAPI.getDashboard(),
        statsAPI.getTrends({ days: 7 }),
        statsAPI.getSLAPerformance({ days: 30 }),
      ])
      
      dashboardStats = statsRes.data
      trendData = trendsRes.data.trends
      slaPerformance = slaRes.data
      
      if (trendData && trendData.length > 0) {
        lineChartData = {
          labels: trendData.map(d => dayjs(d.date).format('MM-DD')),
          datasets: [
            {
              label: '总视频数',
              data: trendData.map(d => d.total_videos),
              borderColor: '#3b82f6',
              backgroundColor: 'rgba(59, 130, 246, 0.1)',
              tension: 0.3,
              fill: true,
            },
            {
              label: '人工审核',
              data: trendData.map(d => d.human_reviewed),
              borderColor: '#f59e0b',
              backgroundColor: 'rgba(245, 158, 11, 0.1)',
              tension: 0.3,
              fill: true,
            },
          ],
        }
        
        barChartData = {
          labels: trendData.map(d => dayjs(d.date).format('MM-DD')),
          datasets: [
            {
              label: '通过',
              data: trendData.map(d => d.approved),
              backgroundColor: '#22c55e',
            },
            {
              label: '拒绝',
              data: trendData.map(d => d.rejected),
              backgroundColor: '#ef4444',
            },
          ],
        }
      }
      
      if (dashboardStats) {
        doughnutChartData = {
          labels: ['机审通过', '机审拒绝', '人工通过', '人工拒绝'],
          datasets: [
            {
              data: [
                dashboardStats.auto_approved || 0,
                dashboardStats.auto_rejected || 0,
                dashboardStats.human_approved || 0,
                dashboardStats.human_rejected || 0,
              ],
              backgroundColor: ['#22c55e', '#ef4444', '#3b82f6', '#f59e0b'],
            },
          ],
        }
      }
    } catch (error) {
      console.error('Failed to load dashboard data:', error)
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
</script>

<div class="page-header">
  <h1 class="page-title">仪表盘</h1>
  <p class="text-secondary text-sm mt-1">
    欢迎回来，{$user?.username} | {getRoleLabel($user?.role)}
  </p>
</div>

<div class="page-content">
  {#if loading}
    <div class="loading-state">
      <div class="spinner"></div>
    </div>
  {:else}
    <div class="stats-grid">
      <div class="stat-card">
        <div class="flex items-center justify-between mb-2">
          <span class="text-secondary text-sm">待审核任务</span>
          <Clock size={20} class="text-warning" />
        </div>
        <div class="stat-value">{dashboardStats?.pending_review || 0}</div>
        <div class="stat-label">需要人工审核</div>
      </div>
      
      <div class="stat-card">
        <div class="flex items-center justify-between mb-2">
          <span class="text-secondary text-sm">进行中</span>
          <Activity size={20} class="text-primary" />
        </div>
        <div class="stat-value">{dashboardStats?.in_review || 0}</div>
        <div class="stat-label">正在审核</div>
      </div>
      
      <div class="stat-card">
        <div class="flex items-center justify-between mb-2">
          <span class="text-secondary text-sm">今日通过</span>
          <CheckCircle size={20} class="text-success" />
        </div>
        <div class="stat-value">{(dashboardStats?.auto_approved || 0) + (dashboardStats?.human_approved || 0)}</div>
        <div class="stat-label">机审 + 人审</div>
      </div>
      
      <div class="stat-card">
        <div class="flex items-center justify-between mb-2">
          <span class="text-secondary text-sm">今日拒绝</span>
          <XCircle size={20} class="text-danger" />
        </div>
        <div class="stat-value">{(dashboardStats?.auto_rejected || 0) + (dashboardStats?.human_rejected || 0)}</div>
        <div class="stat-label">机审 + 人审</div>
      </div>
      
      {#if $isSeniorReviewer}
        <div class="stat-card">
          <div class="flex items-center justify-between mb-2">
            <span class="text-secondary text-sm">待处理申诉</span>
            <AlertTriangle size={20} class="text-warning" />
          </div>
          <div class="stat-value">{dashboardStats?.appeals_pending || 0}</div>
          <div class="stat-label">需要处理</div>
        </div>
        
        <div class="stat-card">
          <div class="flex items-center justify-between mb-2">
            <span class="text-secondary text-sm">SLA 达标率</span>
            <TrendingUp size={20} class="text-success" />
          </div>
          <div class="stat-value">{slaPerformance?.sla_rate?.toFixed(1) || 0}%</div>
          <div class="stat-label">近30天</div>
        </div>
      {/if}
    </div>
    
    <div class="grid" style="display: grid; grid-template-columns: 1fr 1fr; gap: 1.5rem; margin-bottom: 1.5rem;">
      <div class="card">
        <div class="card-header">
          <h3 class="font-semibold">7天审核趋势</h3>
        </div>
        <div class="card-body" style="height: 300px;">
          {#if lineChartData}
            <Line data={lineChartData} options={lineChartOptions} />
          {:else}
            <div class="empty-state">
              暂无数据
            </div>
          {/if}
        </div>
      </div>
      
      <div class="card">
        <div class="card-header">
          <h3 class="font-semibold">每日通过/拒绝对比</h3>
        </div>
        <div class="card-body" style="height: 300px;">
          {#if barChartData}
            <Bar data={barChartData} options={barChartOptions} />
          {:else}
            <div class="empty-state">
              暂无数据
            </div>
          {/if}
        </div>
      </div>
    </div>
    
    {#if $isSeniorReviewer}
      <div class="grid" style="display: grid; grid-template-columns: 1fr 1fr; gap: 1.5rem;">
        <div class="card">
          <div class="card-header">
            <h3 class="font-semibold">审核结果分布</h3>
          </div>
          <div class="card-body" style="height: 300px;">
            {#if doughnutChartData}
              <Doughnut data={doughnutChartData} options={doughnutChartOptions} />
            {:else}
              <div class="empty-state">
                暂无数据
              </div>
            {/if}
          </div>
        </div>
        
        <div class="card">
          <div class="card-header">
            <h3 class="font-semibold">快速操作</h3>
          </div>
          <div class="card-body">
            <div class="space-y-2">
              <a href="/tasks?status=need_review" class="btn btn-secondary w-full justify-start">
                <ListTodo size={18} />
                查看待审核任务
              </a>
              <a href="/my-tasks" class="btn btn-secondary w-full justify-start">
                <ClipboardList size={18} />
                我的待办任务
              </a>
              <a href="/appeals" class="btn btn-secondary w-full justify-start">
                <AlertTriangle size={18} />
                处理申诉
              </a>
              <a href="/statistics" class="btn btn-secondary w-full justify-start">
                <BarChart3 size={18} />
                查看统计报表
              </a>
            </div>
          </div>
        </div>
      </div>
    {/if}
  {/if}
</div>

<script context="module">
  import { ListTodo, ClipboardList, BarChart3 } from 'lucide-svelte'
  
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
  .gap-4 {
    gap: 1rem;
  }
  .space-y-2 > * + * {
    margin-top: 0.5rem;
  }
  .w-full {
    width: 100%;
  }
  .justify-start {
    justify-content: flex-start;
  }
</style>
