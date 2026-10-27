<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

import { resultText } from "../api";
import type {
  Block,
  FactItem,
  Job,
  MeterItem,
  ResultAction,
  ResultSection,
  StatusCell,
  StatusLegendItem,
  ToolDefinition,
} from "../types";
import HeroAlert from "../ui/HeroAlert.vue";
import HeroButton from "../ui/HeroButton.vue";
import HeroCard from "../ui/HeroCard.vue";
import HeroChip from "../ui/HeroChip.vue";
import HeroKbd from "../ui/HeroKbd.vue";
import HeroTable from "../ui/HeroTable.vue";
import MetricView from "./MetricView.vue";
import StatusGrid from "./StatusGrid.vue";

const props = defineProps<{
  job?: Job;
  tool?: ToolDefinition;
}>();

const copied = ref("");
const logsOpen = ref(false);
const content = ref<HTMLElement>();
const emit = defineEmits<{
  action: [action: ResultAction];
}>();

const progress = computed(() => {
  if (!props.job) return 0;
  const { done, total } = props.job.progress;
  return total > 0 ? Math.min(100, (done / total) * 100) : 0;
});

const elapsed = computed(() => {
  const job = props.job;
  if (!job?.startedAt) return "";
  const end = job.finishedAt ? new Date(job.finishedAt) : new Date();
  const ms = end.getTime() - new Date(job.startedAt).getTime();
  if (ms < 1000) return `${Math.max(0, ms)} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
});

const streamedSections = computed(() => props.job?.sections ?? []);
// System inventory reports many small sections. Render them as one continuous
// surface instead of a grid of cramped cards.
const stacked = computed(() => streamedSections.value.length > 2);

// Light sections (a couple of gauges or a short fact list) only need half a
// row; tables, capacity bars and long fact lists take the full width.
function isLightSection(section: ResultSection) {
  const blocks = section.blocks ?? [];
  if (blocks.length === 0) return true;
  if (blocks.length > 1) return false;
  const block = blocks[0] as Block | undefined;
  if (!block) return true;
  if (block.kind === "notice") return true;
  if (block.kind === "facts") {
    const items = factItems(block);
    return items.length > 0 && items.length <= 4;
  }
  if (block.kind === "meter") {
    const items = meterItems(block);
    if (items.some((item) => item.variant === "bar")) return false;
    return items.length <= 2 && meterFacts(block).length <= 2;
  }
  return false;
}
const activeSectionID = computed(() => {
  const sections = streamedSections.value;
  return (
    sections.find((section) => section.state === "running")?.id ??
    sections.find((section) => section.state === "pending")?.id ??
    sections[sections.length - 1]?.id ??
    ""
  );
});

function json(block: Block) {
  return JSON.stringify(block.data, null, 2);
}

function gridCells(block: Block) {
  const data = block.data as { cells?: unknown } | undefined;
  return Array.isArray(data?.cells) ? (data.cells as StatusCell[]) : [];
}

function gridLegend(block: Block) {
  const data = block.data as { legend?: unknown } | undefined;
  return Array.isArray(data?.legend) ? (data.legend as StatusLegendItem[]) : [];
}

function meterItems(block: Block) {
  const data = block.data as { items?: unknown } | undefined;
  return Array.isArray(data?.items) ? (data.items as MeterItem[]) : [];
}

function factItems(block: Block) {
  const data = block.data as { items?: unknown } | undefined;
  return Array.isArray(data?.items) ? (data.items as FactItem[]) : [];
}

// Meter blocks may carry facts alongside their gauges, e.g. memory modules.
function meterFacts(block: Block) {
  const data = block.data as { facts?: unknown } | undefined;
  return Array.isArray(data?.facts) ? (data.facts as FactItem[]) : [];
}

async function copy(text: string, key: string) {
  await navigator.clipboard.writeText(text);
  copied.value = key;
  window.setTimeout(() => {
    if (copied.value === key) copied.value = "";
  }, 1400);
}

async function exportResult(job: Job) {
  if (!job.result) return;
  const blob = new Blob([resultText(job.result)], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `${job.toolId}-${job.id}.txt`;
  link.click();
  URL.revokeObjectURL(url);
  copied.value = "export";
  window.setTimeout(() => {
    if (copied.value === "export") copied.value = "";
  }, 1400);
}

function statusLabel(status: string) {
  return (
    {
      queued: "排队中",
      running: "运行中",
      canceling: "正在停止",
      succeeded: "已完成",
      failed: "失败",
      canceled: "已取消",
    }[status] ?? status
  );
}

function statusColor(status: string): "accent" | "success" | "danger" | "default" {
  if (status === "succeeded") return "success";
  if (status === "failed") return "danger";
  if (status === "queued" || status === "running" || status === "canceling") return "accent";
  return "default";
}

function tone(status?: string): "default" | "accent" | "success" | "warning" | "danger" {
  if (status === "success") return "success";
  if (status === "warning") return "warning";
  if (status === "danger") return "danger";
  if (status === "accent") return "accent";
  return "default";
}

function sectionStateLabel(state: ResultSection["state"]) {
  return {
    pending: "等待检测",
    running: "检测中",
    ready: "已完成",
    skipped: "已跳过",
  }[state];
}

watch(
  () => `${props.job?.id ?? ""}:${props.job?.status ?? ""}`,
  async () => {
    await nextTick();
    if (props.job?.status === "running" || props.job?.status === "succeeded") {
      content.value?.scrollTo({ top: 0 });
    }
  },
  { flush: "post" },
);

watch(
  () => `${activeSectionID.value}:${props.job?.status ?? ""}`,
  async () => {
    if (props.job?.status !== "running") return;
    await nextTick();
    const running = content.value?.querySelector<HTMLElement>(".result-stream-item.state-running");
    const pending = content.value?.querySelector<HTMLElement>(".result-stream-item.state-pending");
    (running ?? pending)?.scrollIntoView({ block: "nearest" });
  },
  { flush: "post" },
);
</script>

<template>
  <HeroCard class="result-pane" variant="default">
    <header class="panel-head result-panel-head">
      <div class="panel-title">
        <span class="panel-index">02</span>
        <div>
          <h2>运行结果</h2>
          <p v-if="job" class="result-job-id mono">{{ job.id }}</p>
          <p v-else>输出、日志与后续操作</p>
        </div>
      </div>
      <div v-if="job" class="result-head-status">
        <HeroChip :color="statusColor(job.status)" variant="soft" size="sm">
          {{ statusLabel(job.status) }}
        </HeroChip>
        <span v-if="elapsed" class="result-meta mono">{{ elapsed }}</span>
      </div>
      <span v-else class="panel-meta mono">尚未运行</span>
    </header>

    <template v-if="job">
      <div
        v-if="job.status === 'running' || job.status === 'canceling' || job.status === 'queued'"
        class="progress-track"
        :class="{ indeterminate: job.progress.total === 0 }"
      >
        <span :style="{ width: `${progress}%` }" />
      </div>

      <div
        v-if="streamedSections.length"
        ref="content"
        class="result-content streamlined"
        :class="{ 'result-content--stacked': stacked }"
      >
        <p v-if="job.result" class="result-summary compact">{{ job.result.summary }}</p>
        <div class="result-stream">
          <section
            v-for="(section, sectionIndex) in streamedSections"
            :key="section.id"
            class="result-stream-item"
            :class="[`state-${section.state}`, { 'result-stream-item--light': stacked && isLightSection(section) }]"
          >
            <header class="result-stream-head">
              <div class="result-stream-title">
                <span class="result-stream-index">{{ (sectionIndex + 1).toString().padStart(2, "0") }}</span>
                <h3>{{ section.title }}</h3>
              </div>
              <span class="result-stream-state" :class="section.state">
                <span v-if="section.state === 'running'" class="section-spinner" />
                <span v-else class="section-state-dot" />
                {{ sectionStateLabel(section.state) }}
              </span>
            </header>
            <div v-if="section.state === 'pending'" class="result-stream-placeholder" aria-hidden="true">
              <span />
              <span />
              <span />
            </div>
            <p v-if="section.text" class="result-stream-text">{{ section.text }}</p>
            <template v-if="section.state !== 'pending'">
              <div v-for="(block, index) in section.blocks ?? []" :key="index" class="result-block">
                <div v-if="block.title && block.title !== section.title" class="block-title">
                  <span>{{ block.title }}</span>
                </div>
                <HeroAlert v-if="block.kind === 'notice'" :status="tone(block.tone)">
                  {{ block.text }}
                </HeroAlert>
                <StatusGrid
                  v-else-if="block.kind === 'status-grid'"
                  :cells="gridCells(block)"
                  :legend="gridLegend(block)"
                  :label="block.title || section.title"
                />
                <MetricView
                  v-else-if="block.kind === 'meter'"
                  :metrics="meterItems(block)"
                  :facts="meterFacts(block)"
                  :label="block.title || section.title"
                />
                <MetricView
                  v-else-if="block.kind === 'facts'"
                  :facts="factItems(block)"
                  :label="block.title || section.title"
                />
                <p v-else-if="block.kind === 'text'" class="block-text">{{ block.text }}</p>
                <pre
                  v-else-if="block.kind === 'code' || block.kind === 'json' || block.kind === 'diff'"
                  class="code-block"
                  :class="{ 'code-block--inline-list': block.language === 'text' }"
                ><code>{{ block.kind === "json" ? json(block) : block.text }}</code></pre>
                <HeroTable
                  v-else-if="block.kind === 'table'"
                  :columns="block.columns ?? []"
                  :rows="block.rows ?? []"
                  :wide="(block.columns?.length ?? 0) > 5"
                  :label="block.title || section.title"
                />
              </div>
            </template>
          </section>
        </div>

        <div v-if="job.result?.warnings?.length" class="result-warnings">
          <HeroAlert
            v-for="warning in job.result.warnings"
            :key="warning"
            status="warning"
          >
            {{ warning }}
          </HeroAlert>
        </div>
        <div v-if="job.result?.actions?.length" class="result-actions">
          <span class="result-section-label">下一步</span>
          <div class="action-grid">
            <HeroButton
              v-for="(action, index) in job.result.actions"
              :key="`${action.label}-${index}`"
              :variant="action.tone === 'danger' ? 'danger-soft' : action.tone === 'success' ? 'secondary' : 'outline'"
              size="sm"
              @click="emit('action', action)"
            >
              {{ action.label }}
            </HeroButton>
          </div>
        </div>
        <div v-if="job.result" class="result-export">
          <HeroButton variant="ghost" size="sm" @click="exportResult(job)">
            {{ copied === "export" ? "已导出" : "导出结果" }}
          </HeroButton>
          <HeroButton variant="secondary" size="sm" @click="copy(resultText(job.result), 'all')">
            {{ copied === "all" ? "已复制" : "复制全部结果" }}
          </HeroButton>
        </div>
      </div>

      <div v-else-if="job.result" class="result-content">
        <p class="result-summary">{{ job.result.summary }}</p>
        <div v-for="(block, index) in job.result.blocks" :key="index" class="result-block">
          <div v-if="block.title" class="block-title">
            <span>{{ block.title }}</span>
            <HeroButton
              v-if="block.kind === 'code' || block.kind === 'json'"
              variant="ghost"
              size="sm"
              @click="copy(block.kind === 'json' ? json(block) : block.text ?? '', String(index))"
            >
              {{ copied === String(index) ? "已复制" : "复制" }}
            </HeroButton>
          </div>

          <HeroAlert
            v-if="block.kind === 'notice'"
            :status="tone(block.tone)"
          >
            {{ block.text }}
          </HeroAlert>

          <StatusGrid
            v-else-if="block.kind === 'status-grid'"
            :cells="gridCells(block)"
            :legend="gridLegend(block)"
            :label="block.title || '状态网格'"
          />

          <MetricView
            v-else-if="block.kind === 'meter'"
            :metrics="meterItems(block)"
            :facts="meterFacts(block)"
            :label="block.title || '指标'"
          />

          <MetricView
            v-else-if="block.kind === 'facts'"
            :facts="factItems(block)"
            :label="block.title || '参数'"
          />

          <p v-else-if="block.kind === 'text'" class="block-text">{{ block.text }}</p>

          <pre
            v-else-if="block.kind === 'code' || block.kind === 'json' || block.kind === 'diff'"
            class="code-block"
          ><code>{{ block.kind === "json" ? json(block) : block.text }}</code></pre>

          <HeroTable
            v-else-if="block.kind === 'table'"
            :columns="block.columns ?? []"
            :rows="block.rows ?? []"
            :wide="(block.columns?.length ?? 0) > 5"
            :label="block.title || '运行结果表格'"
          />
        </div>

        <div v-if="job.result.warnings?.length" class="result-warnings">
          <HeroAlert
            v-for="warning in job.result.warnings"
            :key="warning"
            status="warning"
          >
            {{ warning }}
          </HeroAlert>
        </div>
        <div v-if="job.result.actions?.length" class="result-actions">
          <span class="result-section-label">下一步</span>
          <div class="action-grid">
            <HeroButton
              v-for="(action, index) in job.result.actions"
              :key="`${action.label}-${index}`"
              :variant="action.tone === 'danger' ? 'danger-soft' : action.tone === 'success' ? 'secondary' : 'outline'"
              size="sm"
              @click="emit('action', action)"
            >
              {{ action.label }}
            </HeroButton>
          </div>
        </div>

        <div class="result-export">
          <HeroButton variant="ghost" size="sm" @click="exportResult(job)">
            {{ copied === "export" ? "已导出" : "导出结果" }}
          </HeroButton>
          <HeroButton variant="secondary" size="sm" @click="copy(resultText(job.result), 'all')">
            {{ copied === "all" ? "已复制" : "复制全部结果" }}
          </HeroButton>
        </div>
      </div>

      <div v-if="job.error || job.logs?.length" class="result-tail">
        <HeroAlert v-if="job.error" status="danger" title="执行失败">
          {{ job.error }}
        </HeroAlert>

        <div v-if="job.logs?.length" class="job-log" :class="{ open: logsOpen }">
          <button type="button" class="job-log-toggle" @click="logsOpen = !logsOpen">
            <span>活动记录</span>
            <span class="job-log-meta">
              <span class="mono">{{ job.logs.length }} 行</span>
              <svg viewBox="0 0 12 12" aria-hidden="true"><path d="m3 4.5 3 3 3-3" /></svg>
            </span>
          </button>
          <pre><code><span
            v-for="(entry, index) in job.logs"
            :key="index"
            class="log-line"
            :class="entry.level"
          >{{ new Date(entry.at).toLocaleTimeString("zh-CN", { hour12: false }) }}  {{ entry.message }}
</span></code></pre>
        </div>
      </div>
    </template>

    <div v-else class="result-scroll">
      <div class="result-empty">
        <span class="result-empty-mark">
          <svg viewBox="0 0 32 32" aria-hidden="true">
            <path d="M7 10.5h18M7 16h11M7 21.5h6" />
            <circle cx="24" cy="21.5" r="3.5" />
          </svg>
        </span>
        <div>
          <HeroChip color="default" variant="soft" size="sm">等待运行</HeroChip>
          <h2>结果会显示在这里</h2>
          <p>配置好输入后运行工具，输出、日志与可执行操作会集中呈现在这个面板。</p>
          <div class="command-hint">
            <HeroKbd>Ctrl + Enter</HeroKbd>
            <span>运行当前工具</span>
          </div>
        </div>
      </div>
    </div>
  </HeroCard>
</template>
