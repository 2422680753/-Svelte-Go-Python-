<script>
  import { navigate } from 'svelte-routing'
  import { onMount } from 'svelte'
  import { login, isAuthenticated, showNotification } from '$lib/store'
  import { authAPI } from '$lib/api'
  import { Shield, LogIn, User, Lock, Eye, EyeOff } from 'lucide-svelte'
  
  let isLogin = true
  let loading = false
  let showPassword = false
  
  let loginForm = {
    username: '',
    password: '',
  }
  
  let registerForm = {
    username: '',
    email: '',
    password: '',
    confirmPassword: '',
    department: '',
  }
  
  let formError = ''
  
  onMount(() => {
    if ($isAuthenticated) {
      navigate('/')
    }
  })
  
  async function handleLogin() {
    formError = ''
    loading = true
    
    try {
      const response = await authAPI.login(loginForm)
      login(response.data.token, response.data.user)
      showNotification('登录成功！', 'success')
      navigate('/')
    } catch (error) {
      formError = error.response?.data?.error || '登录失败，请检查用户名和密码'
      showNotification(formError, 'error')
    } finally {
      loading = false
    }
  }
  
  async function handleRegister() {
    formError = ''
    
    if (registerForm.password !== registerForm.confirmPassword) {
      formError = '两次输入的密码不一致'
      return
    }
    
    loading = true
    
    try {
      const response = await authAPI.register({
        username: registerForm.username,
        email: registerForm.email,
        password: registerForm.password,
        department: registerForm.department,
      })
      login(response.data.token, response.data.user)
      showNotification('注册成功！', 'success')
      navigate('/')
    } catch (error) {
      formError = error.response?.data?.error || '注册失败'
      showNotification(formError, 'error')
    } finally {
      loading = false
    }
  }
  
  function toggleMode() {
    isLogin = !isLogin
    formError = ''
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-gradient-to-br from-blue-50 to-indigo-100" style="margin: -1rem;">
  <div class="card" style="width: 100%; max-width: 420px; margin: 1rem;">
    <div class="card-header" style="text-align: center; padding: 2rem;">
      <div style="width: 64px; height: 64px; margin: 0 auto 1rem; background: linear-gradient(135deg, var(--primary-color), #8b5cf6); border-radius: 16px; display: flex; align-items: center; justify-content: center;">
        <Shield size={32} color="white" />
      </div>
      <h1 class="page-title">内容审核平台</h1>
      <p class="text-secondary text-sm mt-1">Content Moderation Platform</p>
    </div>
    
    <div class="card-body" style="padding-top: 0;">
      <div class="flex mb-4">
        <div class="flex" style="background: var(--bg-secondary); border-radius: var(--radius-md); padding: 4px;">
          <button 
            class="flex-1 py-2 px-4 rounded-md text-sm font-medium transition-colors"
            class:bg-white={isLogin}
            class:text-primary={isLogin}
            class:shadow-sm={isLogin}
            class:text-secondary={!isLogin}
            on:click={toggleMode}
          >
            登录
          </button>
          <button 
            class="flex-1 py-2 px-4 rounded-md text-sm font-medium transition-colors"
            class:bg-white={!isLogin}
            class:text-primary={!isLogin}
            class:shadow-sm={!isLogin}
            class:text-secondary={isLogin}
            on:click={toggleMode}
          >
            注册
          </button>
        </div>
      </div>
      
      {#if formError}
        <div class="alert alert-danger mb-4">
          {formError}
        </div>
      {/if}
      
      {#if isLogin}
        <form on:submit|preventDefault={handleLogin}>
          <div class="form-group">
            <label class="form-label">用户名</label>
            <div class="relative">
              <input 
                type="text" 
                class="form-control" 
                bind:value={loginForm.username}
                placeholder="请输入用户名"
                required
              />
              <div class="absolute right-3 top-1/2 -translate-y-1/2 text-muted">
                <User size={18} />
              </div>
            </div>
          </div>
          
          <div class="form-group">
            <label class="form-label">密码</label>
            <div class="relative">
              <input 
                type={showPassword ? 'text' : 'password'} 
                class="form-control" 
                bind:value={loginForm.password}
                placeholder="请输入密码"
                required
              />
              <button 
                type="button"
                class="absolute right-3 top-1/2 -translate-y-1/2 text-muted"
                on:click={() => showPassword = !showPassword}
              >
                {#if showPassword}
                  <EyeOff size={18} />
                {:else}
                  <Eye size={18} />
                {/if}
              </button>
            </div>
          </div>
          
          <button type="submit" class="btn btn-primary w-full btn-lg" disabled={loading}>
            {#if loading}
              <div class="spinner" style="width: 18px; height: 18px;"></div>
            {:else}
              <LogIn size={18} />
            {/if}
            {loading ? '登录中...' : '登录'}
          </button>
        </form>
      {:else}
        <form on:submit|preventDefault={handleRegister}>
          <div class="form-group">
            <label class="form-label">用户名</label>
            <input 
              type="text" 
              class="form-control" 
              bind:value={registerForm.username}
              placeholder="请输入用户名"
              required
            />
          </div>
          
          <div class="form-group">
            <label class="form-label">邮箱</label>
            <input 
              type="email" 
              class="form-control" 
              bind:value={registerForm.email}
              placeholder="请输入邮箱地址"
              required
            />
          </div>
          
          <div class="form-group">
            <label class="form-label">部门</label>
            <select class="form-control" bind:value={registerForm.department}>
              <option value="">请选择部门</option>
              <option value="内容审核">内容审核</option>
              <option value="质量保证">质量保证</option>
              <option value="运营管理">运营管理</option>
              <option value="技术支持">技术支持</option>
            </select>
          </div>
          
          <div class="form-group">
            <label class="form-label">密码</label>
            <input 
              type={showPassword ? 'text' : 'password'} 
              class="form-control" 
              bind:value={registerForm.password}
              placeholder="请输入密码（至少6位）"
              required
            />
          </div>
          
          <div class="form-group">
            <label class="form-label">确认密码</label>
            <input 
              type={showPassword ? 'text' : 'password'} 
              class="form-control" 
              bind:value={registerForm.confirmPassword}
              placeholder="请再次输入密码"
              required
            />
          </div>
          
          <button type="submit" class="btn btn-primary w-full btn-lg" disabled={loading}>
            {#if loading}
              <div class="spinner" style="width: 18px; height: 18px;"></div>
            {:else}
              <LogIn size={18} />
            {/if}
            {loading ? '注册中...' : '注册'}
          </button>
        </form>
      {/if}
      
      <div class="mt-4 text-center text-sm text-muted">
        {#if isLogin}
          还没有账号？<button class="text-primary" on:click={toggleMode}>立即注册</button>
        {:else}
          已有账号？<button class="text-primary" on:click={toggleMode}>立即登录</button>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .relative {
    position: relative;
  }
  .absolute {
    position: absolute;
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
  .min-h-screen {
    min-height: 100vh;
  }
  .flex-1 {
    flex: 1;
  }
  .py-2 {
    padding-top: 0.5rem;
    padding-bottom: 0.5rem;
  }
  .px-4 {
    padding-left: 1rem;
    padding-right: 1rem;
  }
  .rounded-md {
    border-radius: var(--radius-md);
  }
  .transition-colors {
    transition: color, background-color, border-color, text-decoration-color, fill, stroke;
    transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1);
    transition-duration: 150ms;
  }
  .bg-white {
    background-color: white;
  }
  .text-primary {
    color: var(--primary-color);
  }
  .shadow-sm {
    box-shadow: var(--shadow-sm);
  }
  .text-secondary {
    color: var(--text-secondary);
  }
  .text-muted {
    color: var(--text-muted);
  }
  .gradient-to-br {
    background: linear-gradient(to bottom right, var(--tw-gradient-stops));
  }
  .from-blue-50 {
    --tw-gradient-from: #eff6ff;
  }
  .to-indigo-100 {
    --tw-gradient-to: #e0e7ff;
  }
</style>
