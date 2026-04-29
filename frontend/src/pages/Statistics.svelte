<script>
  import { onMount } from 'svelte'
  import { statsAPI } from '$lib/api'
  import { showNotification } from '$lib/store'
  import { 
    TrendingUp, 
    TrendingDown, 
    Clock, 
    CheckCircle, 
    XCircle,
    AlertTriangle,
    Users,
    Video,
    BarChart3,
    PieChart,
    Calendar
  } from 'lucide-svelte'
  import { Bar, Line, Doughnut, PolarArea } from 'svelte-chartjs'
  import {
    Chart as ChartJS,
    CategoryScale,
    LinearScale,
    PointElement,
    LineElement,
    BarElement,
    ArcElement,
    RadialLinearScale,
    Title,
    Tooltip,
    Legend,
    Filler
  } from 'chart.js'
  import dayjs from 'dayjs'

  let loading = true
  let dashboardStats = null
  let trendData = null
  let slaPerformance = null
  let violationStats = null
  let reviewerPerformance = null
  
  let selectedPeriod = 7
  let lineChartData = null
  let barChartData = null
  let doughnutChartData = null
  let polarChartData = null

  ChartJS.register(
    CategoryScale,
    LinearScale,
    PointElement,
    LineElement,
    BarElement,
    ArcElement,
    RadialLinearScale,
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
        position: 'top',
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
      mode: 'index',
      intersect: false,
    },
  }

  const barChartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        position: 'top',
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

  const polarChartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        position: 'right',
      },
    },
  }

  onMount(async () => {
    await loadStatistics()
  })

  async function loadStatistics() {
    loading = true
    try {
      const [statsRes, trendsRes, slaRes, violationsRes, reviewersRes] = await Promise.all([
        statsAPI.getDashboard(),
        statsAPI.getTrends({ days: selectedPeriod }),
        statsAPI.getSLAPerformance({ days: 30 }),
        statsAPI.getViolationStats({
          start_date: dayjs().subtract(30, 'day').format('YYYY-MM-DD'),
          end_date: dayjs().format('YYYY-MM-DD'),
        }),
        statsAPI.getReviewerPerformance({
          start_date: dayjs().subtract(30, 'day').format('YYYY-MM-DD'),
          end_date: dayjs().format('YYYY-MM-DD'),
        }),
      ])
      
      dashboardStats = statsRes.data
      trendData = trendsRes.data.trends
      slaPerformance = slaRes.data
      violationStats = violationsRes.data
      reviewerPerformance = reviewersRes.data
      
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
              label: '机审通过',
              data: trendData.map(d => d.approved - (d.human_reviewed || 0)),
              borderColor: '#22c55e',
              backgroundColor: 'rgba(34, 197, 94, 0.1)',
              tension: 0.3,
              fill: true,
            },
            {
              label: '人工审核',
              data: trendData.map(d => d.human_reviewed || 0),
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
      
      if (violationStats && Object.keys(violationStats).length > 0) {
        const violationLabels = Object.keys(violationStats)
        const violationValues = Object.values(violationStats)
        
        polarChartData = {
          labels: violationLabels.slice(0, 8),
          datasets: [
            {
              data: violationValues.slice(0, 8),
              backgroundColor: [
                '#ef4444',
                '#f97316',
                '#f59e0b',
                '#eab308',
                '#84cc16',
                '#22c55e',
                '#14b8a6',
                '#06b6d4',
              ],
            },
          ],
        }
      }
      
    } catch (error) {
      showNotification('加载统计数据失败', 'error')
    } finally {
      loading = false
    }
  }

  function formatNumber(num) {
    if (num === null || num === undefined) return '0'
    if (num >= 1000000) {
      return (num / 1000000).toFixed(1) + 'M'
    }
    if (num >= 1000) {
      return (num / 1000).toFixed(1) + 'K'
    }
    return num.toString()
  }

  $: isSLAGood = slaPerformance?.sla_rate >= 95
  $: isSLAMedium = slaPerformance?.sla_rate >= 85 && slaPerformance?.sla_rate < 95
</script>

<div class="page-header">
  <div class="flex justify-between items-center">
    <div>
      <h1 class="page-title">统计报表</h1>
      <p class="text-secondary text-sm mt-1">
        内容审核平台数据统计分析
      </p>
    </div>
    <div class="flex gap-2">
      <select 
        class="form-control" 
        bind:value={selectedPeriod}
        on:change={loadStatistics}
        style="width: 120px;"
      >
        <option value={7}>近7天</option>
        <option value={14}>近14天</option>
        <option value={30}>近30天</option>
      </select>
    </div>
  </div>
</div>

<div class="page-content">
  {#if loading}
    <div class="loading-state">
      <div class="spinner"></div>
    </div>
  {:else}
    <div class="stats-grid">
      <div class="stat-card">
        <div class="flex justify-between items-start mb-2">
          <div>
            <div class="text-muted text-sm">总视频数</div>
            <div class="stat-value">{formatNumber(dashboardStats?.total_videos)}</div>
          </div>
          <div class="p-2 bg-blue-50 rounded-lg">
            <Video size={24} class="text-primary" />
          </div>
        </div>
      </div>
      
      <div class="stat-card">
        <div class="flex justify-between items-start mb-2">
          <div>
            <div class="text-muted text-sm">待审核</div>
            <div class="stat-value text-warning">{formatNumber(dashboardStats?.pending_review)}</div>
          </div>
          <div class="p-2 bg-yellow-50 rounded-lg">
            <Clock size={24} class="text-warning" />
          </div>
        </div>
      </div>
      
      <div class="stat-card">
        <div class="flex justify-between items-start mb-2">
          <div>
            <div class="text-muted text-sm">总通过</div>
            <div class="stat-value text-success">
              {formatNumber((dashboardStats?.auto_approved || 0) + (dashboardStats?.human_approved || 0))}
            </div>
          </div>
          <div class="p-2 bg-green-50 rounded-lg">
            <CheckCircle size={24} class="text-success" />
          </div>
        </div>
      </div>
      
      <div class="stat-card">
        <div class="flex justify-between items-start mb-2">
          <div>
            <div class="text-muted text-sm">总拒绝</div>
            <div class="stat-value text-danger">
              {formatNumber((dashboardStats?.auto_rejected || 0) + (dashboardStats?.human_rejected || 0))}
            </div>
          </div>
          <div class="p-2 bg-red-50 rounded-lg">
            <XCircle size={24} class="text-danger" />
          </div>
        </div>
      </div>
      
      <div class="stat-card">
        <div class="flex justify-between items-start mb-2">
          <div>
            <div class="text-muted text-sm">申诉处理中</div>
            <div class="stat-value text-warning">{formatNumber(dashboardStats?.appeals_pending)}</div>
          </div>
          <div class="p-2 bg-orange-50 rounded-lg">
            <AlertTriangle size={24} class="text-warning" />
          </div>
        </div>
      </div>
      
      <div class="stat-card">
        <div class="flex justify-between items-start mb-2">
          <div>
            <div class="text-muted text-sm">SLA 达标率</div>
            <div class="stat-value {isSLAGood ? 'text-success' : isSLAMedium ? 'text-warning' : 'text-danger'}">
              {slaPerformance?.sla_rate?.toFixed(1) || 0}%
            </div>
          </div>
          <div class="p-2 bg-indigo-50 rounded-lg">
            <TrendingUp size={24} class={isSLAGood ? 'text-success' : isSLAMedium ? 'text-warning' : 'text-danger'} />
          </div>
        </div>
        <div class="flex gap-4 text-xs text-muted">
          <span>达标: {formatNumber(slaPerformance?.sla_met)}</span>
          <span>未达标: {formatNumber(slaPerformance?.sla_missed)}</span>
        </div>
      </div>
    </div>
    
    <div class="grid" style="display: grid; grid-template-columns: 1fr 1fr; gap: 1.5rem; margin-bottom: 1.5rem;">
      <div class="card">
        <div class="card-header">
          <h3 class="font-semibold">审核趋势</h3>
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
    
    <div class="grid" style="display: grid; grid-template-columns: 1fr 1fr; gap: 1.5rem; margin-bottom: 1.5rem;">
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
          <h3 class="font-semibold">违规类别分布</h3>
        </div>
        <div class="card-body" style="height: 300px;">
          {#if polarChartData}
            <PolarArea data={polarChartData} options={polarChartOptions} />
          {:else}
            <div class="empty-state">
              暂无数据
            </div>
          {/if}
        </div>
      </div>
    </div>
    
    {#if reviewerPerformance && reviewerPerformance.length > 0}
      <div class="card">
        <div class="card-header">
          <div class="flex justify-between items-center">
            <h3 class="font-semibold">审核员绩效</h3>
            <Users size={18} class="text-muted" />
          </div>
        </div>
        <div class="card-body p-0">
          <table class="table">
            <thead>
              <tr>
                <th>审核员</th>
                <th>总审核数</th>
                <th>通过</th>
                <th>拒绝</th>
                <th>平均用时</th>
                <th>申诉维持</th>
                <th>申诉改判</th>
                <th>准确率</th>
              </tr>
            </thead>
            <tbody>
              {#each reviewerPerformance as perf}
                <tr>
                  <td>
                    <div class="font-medium">{perf.reviewer?.username || '未知'}</div>
                    <div class="text-xs text-muted">
                      {dayjs(perf.date).format('YYYY-MM-DD')}
                    </div>
                  </td>
                  <td class="font-medium">{perf.total_reviews}</td>
                  <td>
                    <span class="text-success">{perf.approved_count}</span>
                  </td>
                  <td>
                    <span class="text-danger">{perf.rejected_count}</span>
                  </td>
                  <td>
                    {perf.average_review_time?.toFixed(1) || '-'}秒
                  </td>
                  <td>
                    <span class="text-success">{perf.appeals_uphold_count}</span>
                  </td>
                  <td>
                    <span class="text-warning">{perf.appeals_overturn_count}</span>
                  </td>
                  <td>
                    <span class="badge {perf.accuracy_score >= 0.9 ? 'badge-success' : perf.accuracy_score >= 0.7 ? 'badge-warning' : 'badge-danger'}">
                      {(perf.accuracy_score * 100)?.toFixed(1) || '-'}%
                    </span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
    
    {#if violationStats && Object.keys(violationStats).length > 0}
      <div class="card mt-4">
        <div class="card-header">
          <div class="flex justify-between items-center">
            <h3 class="font-semibold">违规标签统计</h3>
            <Tag size={18} class="text-muted" />
          </div>
        </div>
        <div class="card-body">
          <div class="grid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 1rem;">
            {#each Object.entries(violationStats) as [tag, count]}
              <div class="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
                <span class="font-medium">{tag}</span>
                <span class="badge badge-danger">{count}</span>
              </div>
            {/each}
          </div>
        </div>
      </div>
    {/if}
  {/if}
</div>

<script context="module">
  import { Tag } from 'lucide-svelte'
</script>

<style>
  .grid {
    display: grid;
  }
  .gap-4 {
    gap: 1rem;
  }
  .gap-2 {
    gap: 0.5rem;
  }
  .flex-1 {
    flex: 1;
  }
  .mt-4 {
    margin-top: 1rem;
  }
  .mb-4 {
    margin-bottom: 1rem;
  }
  .p-2 {
    padding: 0.5rem;
  }
  .p-3 {
    padding: 0.75rem;
  }
  .rounded-lg {
    border-radius: var(--radius-lg);
  }
  .bg-blue-50 {
    background-color: #eff6ff;
  }
  .bg-yellow-50 {
    background-color: #fefce8;
  }
  .bg-green-50 {
    background-color: #f0fdf4;
  }
  .bg-red-50 {
    background-color: #fef2f2;
  }
  .bg-orange-50 {
    background-color: #fff7ed;
  }
  .bg-indigo-50 {
    background-color: #eef2ff;
  }
  .bg-gray-50 {
    background-color: #f9fafb;
  }
  .text-primary {
    color: var(--primary-color);
  }
  .text-success {
    color: var(--success-color);
  }
  .text-warning {
    color: var(--warning-color);
  }
  .text-danger {
    color: var(--danger-color);
  }
  .text-muted {
    color: var(--text-muted);
  }
  .text-secondary {
    color: var(--text-secondary);
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
