<script setup lang="ts">
defineProps<{
  modelValue: boolean;
  label?: string;
}>();

const emit = defineEmits<{ "update:modelValue": [value: boolean] }>();
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="modelValue"
    :aria-label="label"
    class="hero-switch"
    :class="{ 'hero-switch--on': modelValue }"
    @click="emit('update:modelValue', !modelValue)"
  >
    <span />
  </button>
</template>

<style scoped>
.hero-switch {
  position: relative;
  width: 42px;
  height: 24px;
  flex: none;
  border: 0;
  border-radius: 999px;
  background: var(--default);
  padding: 0;
  cursor: var(--cursor-interactive);
  transition: background-color 150ms var(--ease-smooth);
}

.hero-switch span {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--surface);
  box-shadow: 0 1px 4px rgb(0 0 0 / 0.18);
  transition: transform 150ms var(--ease-out);
}

.hero-switch--on {
  background: var(--accent);
}

.hero-switch--on span {
  transform: translateX(18px);
}
</style>
