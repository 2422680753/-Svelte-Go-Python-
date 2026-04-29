<script>
  import { Router, Link, Route } from 'svelte-routing'
  import { onMount } from 'svelte'
  import { user, isAuthenticated } from '$lib/store'
  
  import Login from './pages/Login.svelte'
  import Dashboard from './pages/Dashboard.svelte'
  import TaskList from './pages/TaskList.svelte'
  import TaskReview from './pages/TaskReview.svelte'
  import MyTasks from './pages/MyTasks.svelte'
  import Appeals from './pages/Appeals.svelte'
  import Statistics from './pages/Statistics.svelte'
  import Layout from './components/Layout.svelte'
  import { authAPI } from '$lib/api'

  let loading = true

  onMount(async () => {
    const token = localStorage.getItem('token')
    if (token) {
      try {
        const response = await authAPI.getCurrentUser()
        $user = response.data
        $isAuthenticated = true
      } catch (error) {
        localStorage.removeItem('token')
        localStorage.removeItem('user')
      }
    }
    loading = false
  })

  export let url = ''
</script>

{#if loading}
  <div class="loading-state">
    <div class="spinner"></div>
  </div>
{:else}
  <Router url="{url}">
    <Route path="/login">
      <Login />
    </Route>
    
    <Route path="/">
      {#if $isAuthenticated}
        <Layout>
          <Dashboard />
        </Layout>
      {:else}
        <Login />
      {/if}
    </Route>
    
    <Route path="/tasks">
      {#if $isAuthenticated}
        <Layout>
          <TaskList />
        </Layout>
      {:else}
        <Login />
      {/if}
    </Route>
    
    <Route path="/tasks/:id">
      {#if $isAuthenticated}
        <Layout>
          <TaskReview />
        </Layout>
      {:else}
        <Login />
      {/if}
    </Route>
    
    <Route path="/my-tasks">
      {#if $isAuthenticated}
        <Layout>
          <MyTasks />
        </Layout>
      {:else}
        <Login />
      {/if}
    </Route>
    
    <Route path="/appeals">
      {#if $isAuthenticated}
        <Layout>
          <Appeals />
        </Layout>
      {:else}
        <Login />
      {/if}
    </Route>
    
    <Route path="/statistics">
      {#if $isAuthenticated}
        <Layout>
          <Statistics />
        </Layout>
      {:else}
        <Login />
      {/if}
    </Route>
  </Router>
{/if}
