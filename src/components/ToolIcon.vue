<script setup lang="ts">
defineProps<{
  icon?: string;
  label: string;
}>();
</script>

<template>
  <svg v-if="icon === 'braces'" viewBox="0 0 24 24" aria-hidden="true">
    <path d="M9 4.5H7.8A2.3 2.3 0 0 0 5.5 6.8v2.1c0 1.3-.7 2.2-2 2.6v1c1.3.4 2 1.3 2 2.6v2.1a2.3 2.3 0 0 0 2.3 2.3H9" />
    <path d="M15 4.5h1.2a2.3 2.3 0 0 1 2.3 2.3v2.1c0 1.3.7 2.2 2 2.6v1c-1.3.4-2 1.3-2 2.6v2.1a2.3 2.3 0 0 1-2.3 2.3H15" />
  </svg>
  <svg v-else-if="icon === 'files'" viewBox="0 0 24 24" aria-hidden="true">
    <path d="M8 3.5h5.5l3.5 3.5v10.5A1.5 1.5 0 0 1 15.5 19H8a1.5 1.5 0 0 1-1.5-1.5v-12A2 2 0 0 1 8.5 3.5Z" />
    <path d="M13 3.8V7.5h3.8M5.5 7v11.2A2.8 2.8 0 0 0 8.3 21h6.4" />
  </svg>
  <svg v-else-if="icon === 'network'" viewBox="0 0 24 24" aria-hidden="true">
    <circle cx="12" cy="5" r="2.2" />
    <circle cx="5" cy="18" r="2.2" />
    <circle cx="19" cy="18" r="2.2" />
    <path d="M12 7.2v3.4m0 0-5.3 5.2m5.3-5.2 5.3 5.2" />
  </svg>
  <svg v-else-if="icon === 'system'" viewBox="0 0 24 24" aria-hidden="true">
    <rect x="3.5" y="4.5" width="17" height="12" rx="2" />
    <path d="M8 20h8M12 16.5V20M7 8h4m-4 3h7" />
  </svg>
  <svg v-else-if="icon === 'lan'" viewBox="0 0 24 24" aria-hidden="true">
    <rect x="3.5" y="14.5" width="5" height="5" rx="1" />
    <rect x="15.5" y="14.5" width="5" height="5" rx="1" />
    <rect x="9.5" y="4.5" width="5" height="5" rx="1" />
    <path d="M12 9.5v2.7m-6.5 2.3v-2.3h13v2.3" />
  </svg>
  <span v-else class="tool-icon-fallback">{{ label.slice(0, 1).toUpperCase() }}</span>
</template>
