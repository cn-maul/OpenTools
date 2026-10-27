<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    placeholder?: string;
    rows?: number;
    disabled?: boolean;
    invalid?: boolean;
    fullWidth?: boolean;
    spellcheck?: boolean;
  }>(),
  {
    modelValue: "",
    placeholder: "",
    rows: 6,
    disabled: false,
    invalid: false,
    fullWidth: true,
    spellcheck: false,
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
  focus: [event: FocusEvent];
  blur: [event: FocusEvent];
}>();

const classes = computed(() => [
  "textarea",
  "textarea--primary",
  { "textarea--full-width": props.fullWidth },
]);
</script>

<template>
  <textarea
    :class="classes"
    :value="modelValue"
    :placeholder="placeholder"
    :rows="rows"
    :disabled="disabled"
    :aria-invalid="invalid || undefined"
    :data-invalid="invalid || undefined"
    :spellcheck="spellcheck"
    data-slot="textarea"
    @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    @focus="emit('focus', $event)"
    @blur="emit('blur', $event)"
  />
</template>
