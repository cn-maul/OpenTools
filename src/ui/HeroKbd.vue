<script setup lang="ts">
withDefaults(defineProps<{ variant?: "default" | "light" }>(), { variant: "default" });
</script>

<template>
  <kbd :class="['kbd', `kbd--${variant}`]"><span class="kbd__content"><slot /></span></kbd>
</template>
