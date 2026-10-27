<script setup lang="ts">
import { computed } from "vue";

import SchemaField from "./SchemaField.vue";
import HeroButton from "../ui/HeroButton.vue";
import HeroKbd from "../ui/HeroKbd.vue";
import type { ToolDefinition } from "../types";

const props = defineProps<{
  tool: ToolDefinition;
  model: Record<string, unknown>;
  running: boolean;
  fieldError?: { field: string; text: string } | null;
}>();

const emit = defineEmits<{
  run: [];
  cancel: [];
  changed: [];
}>();

const fields = computed(() =>
  Object.entries(props.tool.input.properties ?? {}).filter(([, schema]) => {
    const condition = schema["x-ui"]?.visibleWhen;
    if (!condition) return true;
    const current = props.model[condition.field];
    if (condition.in?.length) return condition.in.includes(String(current));
    if (condition.equals !== undefined) return current === condition.equals;
    return true;
  }),
);
const required = computed(() => new Set(props.tool.input.required ?? []));
</script>

<template>
  <form class="tool-form" @submit.prevent="emit('run')">
    <div v-if="fields.length" class="form-grid">
      <SchemaField
        v-for="[name, schema] in fields"
        :key="name"
        :name="name"
        :schema="schema"
        :model="model"
        :required="required.has(name)"
        :error="fieldError?.field === name ? fieldError.text : ''"
        :field-error="fieldError"
        @changed="emit('changed')"
      />
    </div>
    <div v-else class="form-empty">
      <span>无需配置</span>
      <p>运行后自动完成检测，并逐项显示结果。</p>
    </div>

    <footer class="form-footer">
      <HeroButton v-if="running" variant="danger" @click="emit('cancel')">
        停止任务
      </HeroButton>
      <HeroButton v-else type="submit">
        运行工具
        <HeroKbd variant="light">Ctrl ↵</HeroKbd>
      </HeroButton>
    </footer>
  </form>
</template>
