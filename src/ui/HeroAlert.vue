<script setup lang="ts">
withDefaults(
  defineProps<{
    status?: "default" | "accent" | "success" | "warning" | "danger";
    title?: string;
  }>(),
  { status: "default", title: "" },
);
</script>

<template>
  <div :class="['alert', `alert--${status}`]" role="alert" data-slot="alert">
    <span class="alert__indicator" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7.5v5.5m0 3.5v.1" />
      </svg>
    </span>
    <div class="alert__content">
      <strong v-if="title" class="alert__title">{{ title }}</strong>
      <div class="alert__description"><slot /></div>
    </div>
  </div>
</template>

<style scoped>
.alert__indicator svg {
  width: 18px;
  height: 18px;
}

.alert__description {
  white-space: pre-wrap;
}
</style>
