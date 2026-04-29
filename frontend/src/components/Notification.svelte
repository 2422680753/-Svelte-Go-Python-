<script>
  import { notification } from '$lib/store'
  import { CheckCircle, XCircle, AlertTriangle, Info, X } from 'lucide-svelte'
  
  $: iconComponent = getIcon($notification?.type)
  
  function getIcon(type) {
    switch (type) {
      case 'success':
        return CheckCircle
      case 'error':
        return XCircle
      case 'warning':
        return AlertTriangle
      default:
        return Info
    }
  }
  
  function getColorClass(type) {
    switch (type) {
      case 'success':
        return 'border-l-4 border-l-green-500'
      case 'error':
        return 'border-l-4 border-l-red-500'
      case 'warning':
        return 'border-l-4 border-l-yellow-500'
      default:
        return 'border-l-4 border-l-blue-500'
    }
  }
</script>

{#if $notification}
  <div class="toast">
    <div class="toast-item {getColorClass($notification.type)}">
      <svelte:component this={iconComponent} size={20} class="text-{$notification.type === 'success' ? 'green-500' : $notification.type === 'error' ? 'red-500' : $notification.type === 'warning' ? 'yellow-500' : 'blue-500'}" />
      <div class="flex-1">
        <div class="text-sm font-medium">{$notification.message}</div>
      </div>
      <button class="modal-close" on:click={() => $notification = null}>
        <X size={16} />
      </button>
    </div>
  </div>
{/if}
