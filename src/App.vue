<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { currentWindow, isMyGo } from "mygo-runtime";

import {
  cancelJob,
  getAppVersion,
  getJob,
  getPluginStore,
  getPluginStoreURL,
  installPlugin,
  listJobs,
  listTools,
  onJobEvent,
  parseFieldError,
  refreshPluginStore,
  savePluginStoreURL,
  startJob,
  uninstallPlugin,
} from "./api";
import ResultView from "./components/ResultView.vue";
import ToolIcon from "./components/ToolIcon.vue";
import ToolForm from "./components/ToolForm.vue";
import type { Job, JobStatus, ResultAction, ToolDefinition, ToolPackage, ToolStore, UISchema } from "./types";
import HeroButton from "./ui/HeroButton.vue";
import HeroCard from "./ui/HeroCard.vue";
import HeroChip from "./ui/HeroChip.vue";
import HeroInput from "./ui/HeroInput.vue";
import HeroKbd from "./ui/HeroKbd.vue";
import HeroModal from "./ui/HeroModal.vue";

const tools = ref<ToolDefinition[]>([]);
const appVersion = ref("0.3.3");
const jobs = ref<Job[]>([]);
const selectedToolId = ref("");
const selectedJobId = ref("");
const search = ref("");
const loading = ref(true);
const error = ref("");
const fieldError = ref<{ field: string; text: string } | null>(null);
const pendingAction = ref<{ tool: ToolDefinition; action: ResultAction } | null>(null);
const values = reactive<Record<string, unknown>>({});
const searchInput = ref<InstanceType<typeof HeroInput>>();
const view = ref<"tools" | "store">("tools");
const store = ref<ToolStore | null>(null);
const storeLoading = ref(false);
const pluginBusy = ref("");
const settingsOpen = ref(false);
const settingsSaving = ref(false);
const settingsError = ref("");
const storeURL = ref("");
const savedStoreURL = ref("");
const theme = ref<"light" | "dark">(
  document.documentElement.dataset.theme === "dark" ? "dark" : "light",
);
let offEvents: (() => void) | undefined;
let refreshTimer: number | undefined;

const selectedTool = computed(() => tools.value.find((tool) => tool.id === selectedToolId.value));
const selectedJob = computed(() => jobs.value.find((job) => job.id === selectedJobId.value));
const runningJob = computed(() => {
  if (selectedJob.value?.status && isActive(selectedJob.value.status)) return selectedJob.value;
  return undefined;
});
const activeJobs = computed(() => jobs.value.filter((job) => isActive(job.status)));
const recentJobs = computed(() => {
  const seen = new Set<string>();
  const installed = new Set(tools.value.map((tool) => tool.id));
  return jobs.value.filter((job) => {
    if (!installed.has(job.toolId)) return false;
    if (seen.has(job.toolId)) return false;
    seen.add(job.toolId);
    return true;
  });
});
const filteredTools = computed(() => {
  const query = search.value.trim().toLowerCase();
  if (!query) return tools.value;
  return tools.value.filter((tool) =>
    [tool.name, tool.summary, tool.category, ...(tool.keywords ?? [])]
      .join(" ")
      .toLowerCase()
      .includes(query),
  );
});
const categories = computed(() => {
  const grouped = new Map<string, ToolDefinition[]>();
  for (const tool of filteredTools.value) {
    const list = grouped.get(tool.category) ?? [];
    list.push(tool);
    grouped.set(tool.category, list);
  }
  return [...grouped.entries()];
});
const installedPackages = computed(() => store.value?.packages.filter((item) => item.installed) ?? []);
const availablePackages = computed(() => {
  const query = search.value.trim().toLowerCase();
  // 已安装且没有新版本的工具不再进入可下载列表。
  const items = (store.value?.packages ?? []).filter(
    (item) => !item.installed || item.updateAvailable,
  );
  if (!query) return items;
  return items.filter((item) =>
    [item.definition.name, item.definition.summary, item.definition.category, item.author ?? ""]
      .join(" ")
      .toLowerCase()
      .includes(query),
  );
});

function isActive(status: JobStatus) {
  return status === "queued" || status === "running" || status === "canceling";
}

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

function replaceValues(next: Record<string, unknown>) {
  for (const key of Object.keys(values)) delete values[key];
  Object.assign(values, next);
}

function selectTool(id: string) {
  view.value = "tools";
  fieldError.value = null;
  selectedToolId.value = id;
  const tool = tools.value.find((item) => item.id === id);
  if (tool) replaceValues(defaults(tool.input));
  const matching = jobs.value.find((job) => job.toolId === id);
  selectedJobId.value = matching?.id ?? "";
}

async function loadStore(refresh = false) {
  storeLoading.value = true;
  error.value = "";
  try {
    store.value = refresh ? await refreshPluginStore() : await getPluginStore();
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    storeLoading.value = false;
  }
}

function showStore() {
  view.value = "store";
  selectedJobId.value = "";
  void loadStore(!store.value);
}

function showSettings() {
  settingsError.value = "";
  storeURL.value = savedStoreURL.value;
  settingsOpen.value = true;
}

function closeSettings() {
  if (settingsSaving.value) return;
  settingsOpen.value = false;
  settingsError.value = "";
}

async function saveSettings() {
  settingsSaving.value = true;
  settingsError.value = "";
  try {
    store.value = await savePluginStoreURL(storeURL.value);
    savedStoreURL.value = storeURL.value.trim();
    storeURL.value = savedStoreURL.value;
    settingsOpen.value = false;
  } catch (cause) {
    settingsError.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    settingsSaving.value = false;
  }
}

function applyTheme(value: "light" | "dark") {
  theme.value = value;
  document.documentElement.dataset.theme = value;
  try {
    localStorage.setItem("opentools-theme", value);
  } catch {
    // Theme still applies when storage is unavailable.
  }
}

function toggleTheme() {
  applyTheme(theme.value === "dark" ? "light" : "dark");
}

async function refreshTools() {
  tools.value = await listTools();
  if (!selectedToolId.value && tools.value[0]) selectTool(tools.value[0].id);
  if (selectedToolId.value && !tools.value.some((tool) => tool.id === selectedToolId.value)) {
    selectedToolId.value = "";
    replaceValues({});
  }
}

async function install(item: ToolPackage) {
  pluginBusy.value = item.definition.id;
  error.value = "";
  try {
    store.value = await installPlugin(item.definition.id);
    await refreshTools();
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    pluginBusy.value = "";
  }
}

async function uninstall(item: ToolPackage) {
  pluginBusy.value = item.definition.id;
  error.value = "";
  try {
    store.value = await uninstallPlugin(item.definition.id);
    await refreshTools();
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    pluginBusy.value = "";
  }
}

function upsertJob(job: Job) {
  const index = jobs.value.findIndex((item) => item.id === job.id);
  if (index >= 0) jobs.value[index] = job;
  else jobs.value.unshift(job);
  jobs.value.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

async function run() {
  if (!selectedTool.value || runningJob.value) return;
  await startWithInput(selectedTool.value, { ...values });
}

async function startWithInput(tool: ToolDefinition, input: Record<string, unknown>) {
  error.value = "";
  fieldError.value = null;
  try {
    const job = await startJob(tool.id, input);
    upsertJob(job);
    selectedJobId.value = job.id;
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : String(cause);
    const parsed = parseFieldError(message);
    if (parsed) {
      fieldError.value = parsed;
    } else {
      error.value = message;
    }
  }
}

async function runAction(action: ResultAction) {
  if (!selectedTool.value || runningJob.value) return;
  if (action.confirm) {
    pendingAction.value = { tool: selectedTool.value, action };
    return;
  }
  await startWithInput(selectedTool.value, { ...values, ...action.input });
}

async function confirmAction() {
  const pending = pendingAction.value;
  if (!pending) return;
  pendingAction.value = null;
  await startWithInput(pending.tool, { ...values, ...pending.action.input });
}

async function cancel() {
  if (!runningJob.value) return;
  try {
    upsertJob(await cancelJob(runningJob.value.id));
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  }
}

async function refreshActive() {
  if (!activeJobs.value.length) return;
  await Promise.all(
    activeJobs.value.map(async (job) => {
      try {
        upsertJob(await getJob(job.id));
      } catch {
        // The event stream remains the primary path.
      }
    }),
  );
}

function loadInput(job: Job) {
  selectedJobId.value = job.id;
  const tool = tools.value.find((item) => item.id === job.toolId);
  if (!tool) return;
  selectedToolId.value = tool.id;
  replaceValues({ ...defaults(tool.input), ...job.input });
}

function onKeydown(event: KeyboardEvent) {
  const command = event.metaKey || event.ctrlKey;
  if (command && event.key === "Enter") {
    event.preventDefault();
    run();
  }
  if (command && event.key.toLowerCase() === "k") {
    event.preventDefault();
    searchInput.value?.focus();
  }
  if (event.key === "Escape" && document.activeElement instanceof HTMLInputElement && document.activeElement.dataset.search === "true") {
    search.value = "";
    searchInput.value?.blur();
  }
}

function minimizeWindow() {
  if (isMyGo()) void currentWindow.minimize();
}

function toggleMaximizeWindow() {
  if (isMyGo()) void currentWindow.toggleMaximize();
}

function closeWindow() {
  if (isMyGo()) void currentWindow.close();
}

function statusDot(status: JobStatus) {
  return {
    queued: "queued",
    running: "active",
    canceling: "active",
    succeeded: "success",
    failed: "failed",
    canceled: "muted",
  }[status];
}

function shortTime(value: string) {
  return new Date(value).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

onMounted(async () => {
  window.addEventListener("keydown", onKeydown);
  offEvents = onJobEvent((job) => {
    upsertJob(job);
  });
  refreshTimer = window.setInterval(refreshActive, 1200);
  // 每项独立加载：单个 RPC 失败不再拖垮整个启动流程。
  const loadedJobs = listJobs().then((result) => {
    jobs.value = result;
  });
  const meta = Promise.all([getAppVersion(), getPluginStoreURL()]).then(
    ([loadedVersion, loadedStoreURL]) => {
      appVersion.value = loadedVersion;
      savedStoreURL.value = loadedStoreURL;
      storeURL.value = loadedStoreURL;
    },
  );
  try {
    const loadedTools = await listTools();
    tools.value = loadedTools;
    if (loadedTools[0]) selectTool(loadedTools[0].id);
    else void loadStore(true);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  }
  await Promise.allSettled([loadedJobs, meta]);
  loading.value = false;
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKeydown);
  offEvents?.();
  if (refreshTimer) window.clearInterval(refreshTimer);
});

watch(selectedToolId, () => {
  error.value = "";
  fieldError.value = null;
  if (selectedToolId.value) view.value = "tools";
});
</script>

<template>
  <div class="shell">
    <header class="titlebar">
      <div class="brand">
        <span class="brand-mark">OT</span>
        <span class="brand-name">OpenTools</span>
        <HeroChip color="default" variant="soft" size="sm">v{{ appVersion }}</HeroChip>
      </div>

      <label class="command">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="11" cy="11" r="6.5" />
          <path d="m16 16 4 4" />
        </svg>
        <HeroInput
          ref="searchInput"
          v-model="search"
          data-search="true"
          placeholder="搜索工具、动作或分类"
          aria-label="搜索工具"
        />
        <HeroKbd>Ctrl K</HeroKbd>
      </label>

      <div class="title-status">
        <span v-if="activeJobs.length" class="running-state">
          <span class="status-pulse active" />
          <span>{{ activeJobs.length }} 个任务运行中</span>
        </span>
        <div class="window-controls" aria-label="窗口控制">
          <button type="button" title="最小化" aria-label="最小化" @click="minimizeWindow">
            <svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2 6h8" /></svg>
          </button>
          <button type="button" title="最大化或还原" aria-label="最大化或还原" @click="toggleMaximizeWindow">
            <svg viewBox="0 0 12 12" aria-hidden="true"><rect x="2.5" y="2.5" width="7" height="7" rx="0.5" /></svg>
          </button>
          <button type="button" class="close" title="关闭" aria-label="关闭" @click="closeWindow">
            <svg viewBox="0 0 12 12" aria-hidden="true"><path d="m2.5 2.5 7 7m0-7-7 7" /></svg>
          </button>
        </div>
      </div>
    </header>

    <div class="workspace">
      <aside class="rail">
        <div class="rail-head">
          <div class="rail-tabs">
            <button type="button" :class="{ active: view === 'tools' }" @click="view = 'tools'">工具</button>
            <button type="button" :class="{ active: view === 'store' }" @click="showStore">商店</button>
          </div>
          <span class="mono count">{{ (view === "tools" ? tools.length : availablePackages.length).toString().padStart(2, "0") }}</span>
        </div>
        <nav v-if="view === 'tools'" class="tool-nav" aria-label="工具">
          <section v-for="[category, items] in categories" :key="category" class="tool-group">
            <h2>{{ category }}</h2>
            <button
              v-for="tool in items"
              :key="tool.id"
              type="button"
              class="tool-link"
              :class="{ selected: selectedToolId === tool.id }"
              @click="selectTool(tool.id)"
            >
              <span class="tool-glyph">
                <ToolIcon :icon="tool.icon" :label="tool.name" />
              </span>
              <span class="tool-copy">
                <span class="tool-name-row">
                  <span class="tool-name">{{ tool.name }}</span>
                  <HeroChip color="default" variant="soft" size="sm">v{{ tool.version }}</HeroChip>
                </span>
                <span class="tool-summary">{{ tool.summary }}</span>
              </span>
            </button>
          </section>
          <p v-if="!categories.length" class="rail-empty">没有匹配的工具。</p>
        </nav>
        <nav v-else class="tool-nav" aria-label="工具商店">
          <section class="tool-group">
            <h2>可获取</h2>
            <button
              v-for="item in availablePackages"
              :key="item.definition.id"
              type="button"
              class="tool-link"
              :class="{ selected: selectedToolId === item.definition.id && item.installed }"
              @click="item.installed ? selectTool(item.definition.id) : undefined"
            >
              <span class="tool-glyph">
                <ToolIcon :icon="item.definition.icon" :label="item.definition.name" />
              </span>
              <span class="tool-copy">
                <span class="tool-name-row">
                  <span class="tool-name">{{ item.definition.name }}</span>
                  <HeroChip :color="item.installed ? 'success' : 'default'" variant="soft" size="sm">
                    {{ item.installed ? "已安装" : "可下载" }}
                  </HeroChip>
                </span>
                <span class="tool-summary">
                  {{ item.definition.summary }}
                </span>
              </span>
            </button>
            <p v-if="!availablePackages.length" class="rail-empty">
              {{ storeLoading ? "正在获取商店目录…" : "商店暂无可用工具。" }}
            </p>
          </section>
        </nav>

        <div class="history">
          <div class="rail-head">
            <span class="eyebrow">最近运行</span>
            <span class="mono count">{{ recentJobs.length.toString().padStart(2, "0") }}</span>
          </div>
          <button
            v-for="job in recentJobs.slice(0, 12)"
            :key="job.id"
            type="button"
            class="history-item"
            :class="{ selected: selectedJobId === job.id }"
            @click="loadInput(job)"
          >
            <span class="history-dot" :class="statusDot(job.status)" />
            <span class="history-tool">{{ tools.find((tool) => tool.id === job.toolId)?.name ?? job.toolId }}</span>
            <span class="history-time mono">{{ shortTime(job.createdAt) }}</span>
          </button>
          <p v-if="!jobs.length" class="rail-empty">暂无运行记录。</p>
        </div>

        <div class="rail-utility">
          <button type="button" class="rail-utility-button" @click="showSettings">
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 15.25A3.25 3.25 0 1 0 12 8.75a3.25 3.25 0 0 0 0 6.5Z" />
              <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06A1.7 1.7 0 0 0 15 19.4a1.7 1.7 0 0 0-1.02 1.56V21h-4v-.04A1.7 1.7 0 0 0 8.96 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.56-1.02H3v-4h.04A1.7 1.7 0 0 0 4.6 8.96a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-1.56V3h4v.04A1.7 1.7 0 0 0 15.04 4.6a1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.18.63.74 1.03 1.56 1H21v4h-.04a1.7 1.7 0 0 0-1.56 1Z" />
            </svg>
            <span>设置</span>
          </button>
          <button type="button" class="rail-utility-button" @click="toggleTheme">
            <svg v-if="theme === 'dark'" viewBox="0 0 24 24" aria-hidden="true">
              <circle cx="12" cy="12" r="3.8" />
              <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4" />
            </svg>
            <svg v-else viewBox="0 0 24 24" aria-hidden="true">
              <path d="M20.1 15.2A8.4 8.4 0 0 1 8.8 3.9 8.5 8.5 0 1 0 20.1 15.2Z" />
            </svg>
            <span>{{ theme === "dark" ? "亮色模式" : "暗色模式" }}</span>
          </button>
        </div>
      </aside>

      <main class="workbench">
        <div v-if="loading" class="loading-state">
          <span class="loading-line" />
          <span class="eyebrow">正在加载本地工具</span>
        </div>

        <template v-else-if="view === 'store'">
          <header class="store-header">
            <div>
              <span class="eyebrow">OpenTools Store</span>
              <h1>工具商店</h1>
              <p>核心只提供运行框架。工具按需下载，独立更新，也可以随时卸载。</p>
            </div>
            <div class="store-actions">
              <HeroButton variant="secondary" :disabled="storeLoading" @click="loadStore(true)">
                {{ storeLoading ? "正在刷新…" : "刷新目录" }}
              </HeroButton>
            </div>
          </header>

          <div v-if="error" class="banner">
            <HeroChip color="danger" variant="soft" size="sm">操作失败</HeroChip>
            <span>{{ error }}</span>
          </div>

          <div v-if="store?.warnings?.length" class="store-warning">
            <strong>部分目录暂不可用</strong>
            <span v-for="warning in store.warnings" :key="warning">{{ warning }}</span>
          </div>

          <section class="store-section">
            <div class="store-section-head">
              <div>
                <h2>已安装</h2>
                <p>这些工具已下载到本机，可离线使用。</p>
              </div>
              <span class="mono">{{ installedPackages.length.toString().padStart(2, "0") }}</span>
            </div>
            <div v-if="installedPackages.length" class="store-grid">
              <article v-for="item in installedPackages" :key="item.definition.id" class="store-card installed">
                <div class="store-card-head">
                  <span class="store-icon"><ToolIcon :icon="item.definition.icon" :label="item.definition.name" /></span>
                  <div class="store-card-title">
                    <div>
                      <h3>{{ item.definition.name }}</h3>
                      <span class="mono version">v{{ item.installedVersion || item.definition.version }}</span>
                    </div>
                    <p>{{ item.definition.summary }}</p>
                  </div>
                </div>
                <div class="store-meta">
                  <span>{{ item.definition.category }}</span>
                  <span>{{ item.author || "OpenTools" }}</span>
                  <span>{{ item.platform }}</span>
                </div>
                <div class="store-card-actions">
                  <HeroButton size="sm" @click="selectTool(item.definition.id)">打开工具</HeroButton>
                  <HeroButton
                    variant="danger-soft"
                    size="sm"
                    :disabled="pluginBusy === item.definition.id"
                    @click="uninstall(item)"
                  >
                    {{ pluginBusy === item.definition.id ? "卸载中…" : "卸载" }}
                  </HeroButton>
                </div>
              </article>
            </div>
            <div v-else class="store-empty">
              <span class="empty-index">00</span>
              <div>
                <h3>还没有安装工具</h3>
                <p>从下方目录选择一个工具下载，安装完成后会立即出现在左侧。</p>
              </div>
            </div>
          </section>

          <section class="store-section">
            <div class="store-section-head">
              <div>
                <h2>可下载</h2>
                <p>来自配置的工具仓库，安装包会校验 SHA256。</p>
              </div>
              <span class="mono">{{ availablePackages.length.toString().padStart(2, "0") }}</span>
            </div>
            <div v-if="availablePackages.length" class="store-grid">
              <article
                v-for="item in availablePackages"
                :key="item.definition.id"
                class="store-card"
                :class="{ disabled: !item.compatible }"
              >
                <div class="store-card-head">
                  <span class="store-icon"><ToolIcon :icon="item.definition.icon" :label="item.definition.name" /></span>
                  <div class="store-card-title">
                    <div>
                      <h3>{{ item.definition.name }}</h3>
                      <span class="mono version">v{{ item.definition.version }}</span>
                    </div>
                    <p>{{ item.definition.summary }}</p>
                  </div>
                </div>
                <div class="store-meta">
                  <span>{{ item.definition.category }}</span>
                  <span>{{ item.author || "OpenTools" }}</span>
                  <span>{{ item.platform || "未知平台" }}</span>
                </div>
                <div class="store-card-actions">
                  <HeroButton
                    v-if="item.installed && !item.updateAvailable"
                    variant="secondary"
                    size="sm"
                    @click="selectTool(item.definition.id)"
                  >
                    已安装
                  </HeroButton>
                  <HeroButton
                    v-else
                    size="sm"
                    :disabled="pluginBusy === item.definition.id || !item.compatible"
                    @click="install(item)"
                  >
                    {{ pluginBusy === item.definition.id ? "安装中…" : item.updateAvailable ? "更新" : "安装" }}
                  </HeroButton>
                </div>
              </article>
            </div>
            <div v-else class="store-empty">
              <span class="empty-index">--</span>
              <div>
                <h3>{{ storeLoading ? "正在读取目录" : "目录为空" }}</h3>
                <p>联网后可刷新工具目录；已安装工具不受影响。</p>
              </div>
            </div>
          </section>
        </template>

        <template v-else-if="selectedTool">
          <div v-if="error" class="banner">
            <HeroChip color="danger" variant="soft" size="sm">请求失败</HeroChip>
            <span>{{ error }}</span>
          </div>

          <div
            class="workspace-board"
            :class="{ 'workspace-board--direct': !Object.keys(selectedTool.input.properties ?? {}).length }"
          >
            <HeroCard class="form-pane" variant="default">
              <header class="panel-head">
                <div class="tool-inline-title">
                  <span class="tool-inline-icon">
                    <ToolIcon :icon="selectedTool.icon" :label="selectedTool.name" />
                  </span>
                  <div class="tool-inline-copy">
                    <div class="tool-inline-name">
                      <h1>{{ selectedTool.name }}</h1>
                      <HeroChip color="accent" variant="soft" size="sm">v{{ selectedTool.version }}</HeroChip>
                    </div>
                  </div>
                </div>
              </header>
              <ToolForm
                :tool="selectedTool"
                :model="values"
                :running="Boolean(runningJob)"
                :field-error="fieldError"
                @run="run"
                @cancel="cancel"
                @changed="error = ''"
              />
            </HeroCard>
            <ResultView
              :job="selectedJob"
              :tool="selectedTool"
              @action="runAction"
            />
          </div>
        </template>

        <div v-else class="empty-result standalone">
          <span class="empty-index">!</span>
          <div>
            <span class="eyebrow">暂无工具</span>
            <h2>还没有安装工具</h2>
            <p>OpenTools 核心不内置业务工具。前往工具商店下载后即可使用。</p>
            <p v-if="error" class="empty-error">{{ error }}</p>
            <HeroButton class="empty-action" @click="showStore">打开工具商店</HeroButton>
          </div>
        </div>
      </main>
    </div>

    <HeroModal
      :open="Boolean(pendingAction)"
      :title="pendingAction?.action.label ?? '确认操作'"
      :description="pendingAction?.action.confirm ?? ''"
      confirm-label="确认执行"
      @close="pendingAction = null"
      @confirm="confirmAction"
    >
      <p class="confirm-copy">{{ pendingAction?.action.confirm }}</p>
    </HeroModal>

    <HeroModal
      :open="settingsOpen"
      title="设置"
      description="配置 OpenTools 的工具商店来源。"
      :confirm-label="settingsSaving ? '保存中…' : '保存'"
      :confirm-disabled="settingsSaving"
      @close="closeSettings"
      @confirm="saveSettings"
    >
      <div class="settings-form">
        <label class="field settings-field">
          <span class="field-heading">
            <span>工具商店 URL</span>
            <span class="settings-optional">可选</span>
          </span>
          <HeroInput
            v-model="storeURL"
            placeholder="https://example.com/opentools/catalog.json"
            :disabled="settingsSaving"
          />
          <span class="field-description">
            留空时使用程序自带目录。多个地址可用英文分号分隔，也支持本地文件路径。
          </span>
        </label>
        <div v-if="settingsError" class="settings-error" role="alert">
          <HeroChip color="danger" variant="soft" size="sm">保存失败</HeroChip>
          <span>{{ settingsError }}</span>
        </div>
        <div v-if="store?.sources?.length && !settingsError" class="settings-current">
          <span class="eyebrow">当前来源</span>
          <code v-for="source in store.sources" :key="source">{{ source }}</code>
        </div>
      </div>
    </HeroModal>
  </div>
</template>
