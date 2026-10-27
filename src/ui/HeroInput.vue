<script setup lang="ts">
import { computed, ref } from "vue";

const props = withDefaults(
  defineProps<{
    modelValue?: string | number;
    type?: string;
    placeholder?: string;
    disabled?: boolean;
    invalid?: boolean;
    fullWidth?: boolean;
    min?: string | number;
    max?: string | number;
    step?: string | number;
    minlength?: number;
    maxlength?: number;
    autocomplete?: string;
    spellcheck?: boolean;
  }>(),
  {
    modelValue: "",
    type: "text",
    placeholder: "",
    disabled: false,
    invalid: false,
    fullWidth: true,
    min: undefined,
    max: undefined,
    step: undefined,
    minlength: undefined,
    maxlength: undefined,
    autocomplete: "off",
    spellcheck: false,
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
  focus: [event: FocusEvent];
  blur: [event: FocusEvent];
}>();

const classes = computed(() => [
  "input",
  "input--primary",
  { "input--full-width": props.fullWidth },
]);

const input = ref<HTMLInputElement>();

defineExpose({
  blur: () => input.value?.blur(),
  focus: () => input.value?.focus(),
});
</script>

<template>
  <input
    ref="input"
    :class="classes"
    :type="type"
    :value="modelValue"
    :placeholder="placeholder"
    :disabled="disabled"
    :aria-invalid="invalid || undefined"
    :data-invalid="invalid || undefined"
    :min="min"
    :max="max"
    :step="step"
    :minlength="minlength"
    :maxlength="maxlength"
    :autocomplete="autocomplete"
    :spellcheck="spellcheck"
    data-slot="input"
    @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    @focus="emit('focus', $event)"
    @blur="emit('blur', $event)"
  />
</template>
