<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";

const props = withDefaults(
  defineProps<{
    id?: string;
    modelValue?: string;
    options: { value: string; label: string }[];
    disabled?: boolean;
    fullWidth?: boolean;
  }>(),
  {
    id: undefined,
    modelValue: "",
    disabled: false,
    fullWidth: true,
  },
);

const emit = defineEmits<{ "update:modelValue": [value: string] }>();

const open = ref(false);
const activeIndex = ref(-1);
const root = ref<HTMLElement>();
const trigger = ref<HTMLButtonElement>();
const menu = ref<HTMLElement>();

const selectedIndex = computed(() =>
  props.options.findIndex((option) => option.value === props.modelValue),
);
const selectedLabel = computed(() => props.options[selectedIndex.value]?.label ?? "");

function toggle() {
  if (props.disabled) return;
  if (open.value) {
    close();
    return;
  }
  open.value = true;
  activeIndex.value = selectedIndex.value >= 0 ? selectedIndex.value : 0;
  void nextTick(scrollActiveIntoView);
}

function close() {
  open.value = false;
  activeIndex.value = -1;
}

function choose(index: number) {
  const option = props.options[index];
  if (!option) return;
  emit("update:modelValue", option.value);
  close();
  trigger.value?.focus();
}

function scrollActiveIntoView() {
  const node = menu.value?.children[activeIndex.value] as HTMLElement | undefined;
  node?.scrollIntoView({ block: "nearest" });
}

function onKeydown(event: KeyboardEvent) {
  if (props.disabled) return;
  if (event.key === "Escape") {
    if (open.value) {
      event.preventDefault();
      close();
    }
    return;
  }
  if (!open.value) {
    if (event.key === "Enter" || event.key === " " || event.key === "ArrowDown") {
      event.preventDefault();
      toggle();
    }
    return;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    activeIndex.value = Math.min(activeIndex.value + 1, props.options.length - 1);
    void nextTick(scrollActiveIntoView);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    activeIndex.value = Math.max(activeIndex.value - 1, 0);
    void nextTick(scrollActiveIntoView);
  } else if (event.key === "Enter") {
    event.preventDefault();
    choose(activeIndex.value);
  }
}

// The menu lives inside the field, so close it when clicking anywhere else.
function onDocumentPointer(event: PointerEvent) {
  if (!open.value) return;
  if (root.value && !root.value.contains(event.target as Node)) close();
}

watch(open, (value) => {
  if (value) document.addEventListener("pointerdown", onDocumentPointer);
  else document.removeEventListener("pointerdown", onDocumentPointer);
});

onBeforeUnmount(() => document.removeEventListener("pointerdown", onDocumentPointer));
</script>

<template>
  <div
    ref="root"
    :class="['select', 'select--primary', { 'select--full-width': fullWidth }]"
    data-slot="select"
    @keydown="onKeydown"
  >
    <button
      :id="id"
      ref="trigger"
      type="button"
      class="select__trigger"
      :disabled="disabled"
      :aria-expanded="open"
      aria-haspopup="listbox"
      @click="toggle"
    >
      <span class="select__value">{{ selectedLabel }}</span>
      <span class="select__indicator" data-slot="select-default-indicator" aria-hidden="true">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="m4 6 4 4 4-4" />
        </svg>
      </span>
    </button>
    <ul v-if="open" ref="menu" class="select__menu" role="listbox">
      <li
        v-for="(option, index) in options"
        :key="option.value"
        class="select__option"
        :class="{
          'select__option--active': index === activeIndex,
          'select__option--selected': option.value === modelValue,
        }"
        role="option"
        :aria-selected="option.value === modelValue"
        @mouseenter="activeIndex = index"
        @click="choose(index)"
      >
        {{ option.label }}
      </li>
    </ul>
  </div>
</template>

<style scoped>
.select {
  position: relative;
  min-width: 0;
  outline: none;
}

.select__trigger {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border: 1px solid color-mix(in oklab, var(--border) 72%, transparent);
  border-radius: 12px;
  background: var(--surface-secondary);
  color: var(--field-foreground);
  cursor: pointer;
  padding: 7px 10px;
  text-align: left;
  transition:
    border-color 150ms ease,
    background-color 150ms ease;
}

.select__trigger:hover {
  border-color: color-mix(in oklab, var(--foreground) 22%, var(--border));
}

.select__trigger:focus-visible {
  border-color: color-mix(in oklab, var(--accent) 55%, var(--border));
}

.select__trigger:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.select__value {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  font-size: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.select__indicator {
  flex: 0 0 auto;
  color: var(--muted);
  transition: transform 150ms ease;
}

.select__indicator svg {
  display: block;
  width: 16px;
  height: 16px;
}

.select__trigger[aria-expanded="true"] .select__indicator {
  transform: rotate(180deg);
}

.select__menu {
  position: absolute;
  z-index: 90;
  top: calc(100% + 6px);
  right: 0;
  left: 0;
  max-height: 280px;
  margin: 0;
  padding: 5px;
  overflow-y: auto;
  border: 1px solid color-mix(in oklab, var(--foreground) 14%, transparent);
  border-radius: 12px;
  background: var(--overlay);
  box-shadow: 0 12px 30px rgb(0 0 0 / 0.22);
  list-style: none;
}

.select__option {
  border-radius: 8px;
  color: var(--overlay-foreground, var(--foreground));
  cursor: pointer;
  font-size: 14px;
  padding: 7px 9px;
  white-space: nowrap;
}

.select__option--active {
  background: color-mix(in oklab, var(--foreground) 9%, transparent);
}

.select__option--selected {
  color: var(--accent);
  font-weight: 600;
}
</style>
