<script setup lang="ts">
withDefaults(
  defineProps<{
    color?: "default" | "accent" | "success" | "warning" | "danger";
    variant?: "primary" | "secondary" | "tertiary" | "soft";
    size?: "sm" | "md" | "lg";
  }>(),
  { color: "default", variant: "soft", size: "md" },
);
</script>

<template>
  <span :class="['chip', `chip--${color}`, `chip--${variant}`, `chip--${size}`]">
    <span class="chip__label"><slot /></span>
  </span>
</template>
