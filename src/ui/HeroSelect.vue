<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    options: { value: string; label: string }[];
    disabled?: boolean;
    fullWidth?: boolean;
  }>(),
  {
    modelValue: "",
    disabled: false,
    fullWidth: true,
  },
);

const emit = defineEmits<{ "update:modelValue": [value: string] }>();
const selectedLabel = computed(
  () => props.options.find((option) => option.value === props.modelValue)?.label ?? "",
);
</script>

<template>
  <label :class="['select', 'select--primary', { 'select--full-width': fullWidth }]" data-slot="select">
    <span class="select__trigger">
      <span class="select__value">{{ selectedLabel }}</span>
      <span class="select__indicator" data-slot="select-default-indicator" aria-hidden="true">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="m4 6 4 4 4-4" />
        </svg>
      </span>
      <select
        :value="modelValue"
        :disabled="disabled"
        @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)"
      >
        <option v-for="option in options" :key="option.value" :value="option.value">
          {{ option.label }}
        </option>
      </select>
    </span>
  </label>
</template>

<style scoped>
.select__trigger {
  position: relative;
  width: 100%;
}

.select__trigger select {
  position: absolute;
  inset: 0;
  width: 100%;
  cursor: pointer;
  opacity: 0;
}

.select__indicator svg {
  width: 16px;
  height: 16px;
}
</style>
