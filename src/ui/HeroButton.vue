<script setup lang="ts">
import { computed } from "vue";

type Variant =
  | "primary"
  | "secondary"
  | "tertiary"
  | "ghost"
  | "outline"
  | "danger"
  | "danger-soft";

const props = withDefaults(
  defineProps<{
    variant?: Variant;
    size?: "sm" | "md" | "lg";
    fullWidth?: boolean;
    iconOnly?: boolean;
    disabled?: boolean;
    type?: "button" | "submit" | "reset";
  }>(),
  {
    variant: "primary",
    size: "md",
    fullWidth: false,
    iconOnly: false,
    disabled: false,
    type: "button",
  },
);

const classes = computed(() => [
  "button",
  `button--${props.variant}`,
  `button--${props.size}`,
  { "button--full-width": props.fullWidth, "button--icon-only": props.iconOnly },
]);
</script>

<template>
  <button
    :type="type"
    :class="classes"
    :disabled="disabled"
    :aria-disabled="disabled || undefined"
    data-slot="button"
  >
    <slot />
  </button>
</template>
