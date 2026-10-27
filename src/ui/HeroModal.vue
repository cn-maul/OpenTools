<script setup lang="ts">
import { onBeforeUnmount, watch } from "vue";

import HeroButton from "./HeroButton.vue";

const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    description?: string;
    confirmLabel?: string;
    cancelLabel?: string;
    confirmDisabled?: boolean;
  }>(),
  {
    description: "",
    confirmLabel: "确认",
    cancelLabel: "取消",
    confirmDisabled: false,
  },
);

const emit = defineEmits<{ close: []; confirm: [] }>();

function close() {
  emit("close");
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === "Escape" && props.open) close();
}

watch(
  () => props.open,
  (open) => {
    document.documentElement.classList.toggle("ui-modal-open", open);
    if (open) window.addEventListener("keydown", onKeydown);
    else window.removeEventListener("keydown", onKeydown);
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  document.documentElement.classList.remove("ui-modal-open");
  window.removeEventListener("keydown", onKeydown);
});
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="modal__backdrop modal__backdrop--blur"
      data-slot="modal-backdrop"
      @click.self="close"
    >
      <div class="modal__container" data-placement="center" data-slot="modal-container">
        <section
          class="modal__dialog modal__dialog--md modal__dialog--scroll-inside"
          data-placement="center"
          role="dialog"
          aria-modal="true"
          :aria-label="title"
          data-slot="modal-dialog"
        >
          <header class="modal__header">
            <h2 class="modal__heading">{{ title }}</h2>
          </header>
          <div class="modal__body modal__body--scroll-inside">
            <slot>{{ description }}</slot>
          </div>
          <footer class="modal__footer">
            <HeroButton variant="secondary" @click="close">{{ cancelLabel }}</HeroButton>
            <HeroButton :disabled="confirmDisabled" @click="emit('confirm')">{{ confirmLabel }}</HeroButton>
          </footer>
        </section>
      </div>
    </div>
  </Teleport>
</template>
