<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { chooseDirectory, chooseFile, readTextFile } from "../api";
import type { UISchema } from "../types";
import HeroButton from "../ui/HeroButton.vue";
import HeroInput from "../ui/HeroInput.vue";
import HeroSelect from "../ui/HeroSelect.vue";
import HeroSwitch from "../ui/HeroSwitch.vue";
import HeroTextarea from "../ui/HeroTextarea.vue";

defineOptions({ name: "SchemaField" });

const props = defineProps<{
  name: string;
  schema: UISchema;
  model: Record<string, unknown>;
  required?: boolean;
  depth?: number;
  error?: string;
  fieldError?: { field: string; text: string } | null;
}>();

const emit = defineEmits<{ changed: [] }>();
const depth = computed(() => props.depth ?? 0);
const value = computed(() => props.model[props.name]);
const enumValues = computed(() => props.schema.enum ?? []);
const enumLabels = computed(() => props.schema["x-ui"]?.enumLabels ?? {});
const textValue = computed({
  get: () => String(value.value ?? ""),
  set: (next: string) => set(next),
});
const numberInputValue = computed({
  get: () => (value.value === "" || value.value === undefined ? "" : Number(value.value)),
  set: (next: string) => set(next === "" ? 0 : Number(next)),
});
const booleanValue = computed({
  get: () => Boolean(value.value),
  set: (next: boolean) => set(next),
});
const arrayInputValue = computed({
  get: () => arrayDraft.value,
  set: (next: string) => updateArrayValue(next),
});
const selectOptions = computed(() =>
  enumValues.value.map((option) => ({
    value: String(option),
    label: String(enumLabels.value[String(option)] ?? option),
  })),
);
const arrayDraft = ref(JSON.stringify(value.value ?? [], null, 2));
const arrayError = ref("");
let lastArrayValue = JSON.stringify(value.value ?? []);

function set(next: unknown) {
  props.model[props.name] = next;
  emit("changed");
}

async function pickDirectory() {
  const path = await chooseDirectory();
  if (path) set(path);
}

async function pickFile() {
  const path = await chooseFile(props.schema["x-ui"]?.accept);
  if (!path) return;
  const widget = props.schema["x-ui"]?.widget;
  if (widget === "code" || props.schema["x-ui"]?.rows) {
    set(await readTextFile(path));
  } else {
    set(path);
  }
}

function updateArrayValue(text: string) {
  arrayDraft.value = text;
  try {
    const parsed = JSON.parse(text);
    if (!Array.isArray(parsed)) throw new Error("必须是 JSON 数组。");
    arrayError.value = "";
    lastArrayValue = JSON.stringify(parsed);
    set(parsed);
  } catch (error) {
    arrayError.value = error instanceof Error ? error.message : String(error);
  }
}

function showArrayDraft() {
  const current = JSON.stringify(value.value ?? []);
  if (current !== lastArrayValue) {
    arrayDraft.value = JSON.stringify(value.value ?? [], null, 2);
    lastArrayValue = current;
  }
}

const widget = computed(() => props.schema["x-ui"]?.widget ?? "");
const placeholder = computed(() => props.schema["x-ui"]?.placeholder ?? "");
const fieldId = computed(() => `field-${props.name}-${depth.value}`);
// A hidden field carries a value without showing a control. It is used by
// result actions that need to pass structured data back into the tool.
const hidden = computed(() => widget.value === "hidden");
const wide = computed(
  () => widget.value === "code" || widget.value === "textarea" || Boolean(props.schema["x-ui"]?.rows),
);

function initializeField() {
  if (props.schema.type !== "object" || value.value !== undefined) return;
  const nested = defaults(props.schema);
  if (Object.keys(nested).length) set(nested);
}

watch(value, initializeField, { immediate: true });

function defaults(schema: UISchema): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [name, child] of Object.entries(schema.properties ?? {})) {
    if (child.default !== undefined) {
      out[name] = structuredClone(child.default);
    } else if (child.type === "boolean") {
      out[name] = false;
    } else if (child.type === "number" || child.type === "integer") {
      out[name] = 0;
    } else if (child.type === "object") {
      out[name] = defaults(child);
    } else if (child.type === "array") {
      out[name] = [];
    } else {
      out[name] = "";
    }
  }
  return out;
}
</script>

<template>
  <input
    v-if="hidden"
    type="hidden"
    :name="name"
    :value="String(value ?? '')"
  />

  <fieldset v-if="schema.type === 'object'" class="schema-object">
    <legend v-if="schema.title">
      {{ schema.title }}
      <span v-if="required" class="required">必填</span>
    </legend>
    <SchemaField
      v-for="(child, childName) in schema.properties"
      :key="childName"
      :name="childName"
      :schema="child"
      :model="(model[name] as Record<string, unknown>) ?? {}"
      :required="schema.required?.includes(childName)"
      :depth="depth + 1"
      :field-error="fieldError"
      @changed="emit('changed')"
    />
  </fieldset>

  <div
    v-else-if="!hidden"
    class="textfield field"
    :class="{
      'textfield--full-width': true,
      'field--wide': wide,
      'field--path': widget === 'directory' || widget === 'file',
    }"
  >
    <div class="field-heading">
      <label :for="fieldId">{{ schema.title || name }}</label>
      <span v-if="required" class="required">必填</span>
    </div>
    <p v-if="schema.description" class="field-description">{{ schema.description }}</p>

    <div v-if="schema.type === 'boolean'" class="switch-row">
      <HeroSwitch :id="fieldId" v-model="booleanValue" :label="schema.title || name" />
      <span class="switch-label">{{ value ? "开启" : "关闭" }}</span>
    </div>

    <HeroSelect
      v-else-if="enumValues.length"
      :id="fieldId"
      v-model="textValue"
      :options="selectOptions"
    />

    <div v-else-if="widget === 'directory' || widget === 'file'" class="path-field">
      <HeroInput
        :id="fieldId"
        v-model="textValue"
        :placeholder="placeholder || (widget === 'directory' ? '选择一个文件夹' : '选择一个文件')"
      />
      <HeroButton
        variant="secondary"
        @click="widget === 'directory' ? pickDirectory() : pickFile()"
      >
        选择
      </HeroButton>
    </div>

    <div v-else-if="widget === 'code'" class="code-input">
      <div class="code-input-head">
        <span>JSON 源码</span>
        <HeroButton variant="secondary" size="sm" @click="pickFile">从文件载入</HeroButton>
      </div>
      <HeroTextarea
        :id="fieldId"
        v-model="textValue"
        :placeholder="placeholder"
        :rows="schema['x-ui']?.rows ?? 12"
      />
    </div>

    <HeroTextarea
      v-else-if="widget === 'textarea' || schema['x-ui']?.rows"
      :id="fieldId"
      v-model="textValue"
      :placeholder="placeholder"
      :rows="schema['x-ui']?.rows ?? 6"
    />

    <HeroInput
      v-else-if="schema.type === 'number' || schema.type === 'integer'"
      :id="fieldId"
      v-model="numberInputValue"
      type="number"
      :min="schema.minimum"
      :max="schema.maximum"
      :step="schema.type === 'integer' ? 1 : 'any'"
    />

    <HeroInput
      v-else-if="schema.type === 'array'"
      :id="fieldId"
      v-model="arrayInputValue"
      class="input--mono"
      :placeholder="placeholder || '[]'"
      @focus="showArrayDraft"
    />

    <HeroInput
      v-else
      :id="fieldId"
      v-model="textValue"
      :type="schema['x-ui']?.secret ? 'password' : 'text'"
      :placeholder="placeholder"
      :minlength="schema.minLength"
      :maxlength="schema.maxLength"
    />
    <p v-if="arrayError" class="field-error">请输入有效的 JSON 数组。</p>
    <p v-if="error" class="field-error">{{ error }}</p>
  </div>
</template>
