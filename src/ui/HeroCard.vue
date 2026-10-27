<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: "default" | "secondary" | "tertiary" | "transparent";
  }>(),
  { variant: "default" },
);
</script>

<template>
  <section :class="['card', `card--${variant}`]" data-slot="card">
    <slot />
  </section>
</template>
