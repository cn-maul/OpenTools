import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button, Card, Chip, Input, Kbd } from "@heroui/react";
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
import AppModal from "./components/AppModal";
import ResultView from "./components/ResultView";
import ToolForm from "./components/ToolForm";
import ToolIcon from "./components/ToolIcon";
import type {
  Job,
  JobStatus,
  ResultAction,
  ToolDefinition,
  ToolPackage,
  ToolStore,
} from "./types";
import { schemaDefaults } from "./components/SchemaField";

const GITEE_STORE_URL =
  "https://gitee.com/dogge2333/opentools-store/raw/master/catalog.json";

type View = "tools" | "store";
type Theme = "light" | "dark";

function isActive(status: JobStatus) {
  return status === "queued" || status === "running" || status === "canceling";
}

function statusDot(status: JobStatus) {
  return (
    {
      queued: "queued",
      running: "active",
      canceling: "active",
      succeeded: "success",
      failed: "failed",
      canceled: "muted",
    }[status] ?? "muted"
  );
}

function shortTime(value: string) {
  return new Date(value).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

function errorText(cause: unknown) {
  return cause instanceof Error ? cause.message : String(cause);
}

export default function App() {
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [appVersion, setAppVersion] = useState("0.3.3");
  const [jobs, setJobs] = useState<Job[]>([]);
  const [selectedToolId, setSelectedToolId] = useState("");
  const [selectedJobId, setSelectedJobId] = useState("");
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [fieldError, setFieldError] = useState<{ field: string; text: string } | null>(null);
  const [pendingAction, setPendingAction] = useState<{
    tool: ToolDefinition;
    action: ResultAction;
  } | null>(null);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [view, setView] = useState<View>("tools");
  const [store, setStore] = useState<ToolStore | null>(null);
  const [storeLoading, setStoreLoading] = useState(false);
  const [pluginBusy, setPluginBusy] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsError, setSettingsError] = useState("");
  const [storeURL, setStoreURL] = useState("");
  const [savedStoreURL, setSavedStoreURL] = useState("");
  const [theme, setTheme] = useState<Theme>(
    document.documentElement.dataset.theme === "dark" ? "dark" : "light",
  );

  const searchRef = useRef<HTMLInputElement>(null);
  const valuesRef = useRef(values);
  valuesRef.current = values;
  const toolsRef = useRef(tools);
  toolsRef.current = tools;
  const jobsRef = useRef(jobs);
  jobsRef.current = jobs;
  const selectedToolIdRef = useRef(selectedToolId);
  selectedToolIdRef.current = selectedToolId;
  const viewRef = useRef(view);
  viewRef.current = view;

  const selectedTool = useMemo(
    () => tools.find((tool) => tool.id === selectedToolId),
    [tools, selectedToolId],
  );
  const selectedJob = useMemo(
    () => jobs.find((job) => job.id === selectedJobId),
    [jobs, selectedJobId],
  );
  const runningJob = selectedJob && isActive(selectedJob.status) ? selectedJob : undefined;
  const activeJobs = useMemo(() => jobs.filter((job) => isActive(job.status)), [jobs]);
  const recentJobs = useMemo(() => {
    const seen = new Set<string>();
    const installed = new Set(tools.map((tool) => tool.id));
    return jobs.filter((job) => {
      if (!installed.has(job.toolId)) return false;
      if (seen.has(job.toolId)) return false;
      seen.add(job.toolId);
      return true;
    });
  }, [jobs, tools]);
  const filteredTools = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return tools;
    return tools.filter((tool) =>
      [tool.name, tool.summary, tool.category, ...(tool.keywords ?? [])]
        .join(" ")
        .toLowerCase()
        .includes(query),
    );
  }, [tools, search]);
  const categories = useMemo(() => {
    const grouped = new Map<string, ToolDefinition[]>();
    for (const tool of filteredTools) {
      const list = grouped.get(tool.category) ?? [];
      list.push(tool);
      grouped.set(tool.category, list);
    }
    return [...grouped.entries()];
  }, [filteredTools]);
  const storePackages = useMemo(() => {
    const all = store?.packages ?? [];
    return [...all.filter((item) => item.installed), ...all.filter((item) => !item.installed)];
  }, [store]);
  const availablePackages = useMemo(() => {
    const query = search.trim().toLowerCase();
    const items = (store?.packages ?? []).filter(
      (item) => !item.installed || item.updateAvailable,
    );
    if (!query) return items;
    return items.filter((item) =>
      [item.definition.name, item.definition.summary, item.definition.category, item.author ?? ""]
        .join(" ")
        .toLowerCase()
        .includes(query),
    );
  }, [store, search]);

  const upsertJob = useCallback((job: Job) => {
    setJobs((previous) => {
      const index = previous.findIndex((item) => item.id === job.id);
      const next = [...previous];
      if (index >= 0) next[index] = job;
      else next.unshift(job);
      next.sort((left, right) => right.createdAt.localeCompare(left.createdAt));
      return next;
    });
  }, []);

  const selectTool = useCallback((id: string) => {
    setView("tools");
    setFieldError(null);
    setSelectedToolId(id);
    const tool = toolsRef.current.find((item) => item.id === id);
    if (tool) setValues(schemaDefaults(tool.input));
    const matching = jobsRef.current.find((job) => job.toolId === id);
    setSelectedJobId(matching?.id ?? "");
  }, []);

  const loadStore = useCallback(async (refresh = false) => {
    setStoreLoading(true);
    setError("");
    try {
      setStore(refresh ? await refreshPluginStore() : await getPluginStore());
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setStoreLoading(false);
    }
  }, []);

  const showStore = useCallback(() => {
    setView("store");
    setSelectedJobId("");
    void loadStore(!store);
  }, [loadStore, store]);

  const applyTheme = useCallback((value: Theme) => {
    setTheme(value);
    document.documentElement.dataset.theme = value;
    try {
      localStorage.setItem("opentools-theme", value);
    } catch {
      // Theme still applies when storage is unavailable.
    }
  }, []);

  const refreshTools = useCallback(async (keepView = false) => {
    const previousView = viewRef.current;
    const loaded = await listTools();
    toolsRef.current = loaded;
    setTools(loaded);
    if (!selectedToolIdRef.current && loaded[0]) selectTool(loaded[0].id);
    if (
      selectedToolIdRef.current &&
      !loaded.some((tool) => tool.id === selectedToolIdRef.current)
    ) {
      setSelectedToolId("");
      setValues({});
    }
    if (keepView) setView(previousView);
  }, [selectTool]);

  const startWithInput = useCallback(
    async (tool: ToolDefinition, input: Record<string, unknown>) => {
      setError("");
      setFieldError(null);
      try {
        const job = await startJob(tool.id, input);
        upsertJob(job);
        setSelectedJobId(job.id);
      } catch (cause) {
        const message = errorText(cause);
        const parsed = parseFieldError(message);
        if (parsed) setFieldError(parsed);
        else setError(message);
      }
    },
    [upsertJob],
  );

  const run = useCallback(() => {
    if (!selectedTool || runningJob) return;
    void startWithInput(selectedTool, { ...valuesRef.current });
  }, [selectedTool, runningJob, startWithInput]);

  const runAction = useCallback(
    async (action: ResultAction) => {
      if (!selectedTool || runningJob) return;
      if (action.confirm) {
        setPendingAction({ tool: selectedTool, action });
        return;
      }
      await startWithInput(selectedTool, { ...valuesRef.current, ...action.input });
    },
    [selectedTool, runningJob, startWithInput],
  );

  const cancel = useCallback(async () => {
    if (!runningJob) return;
    try {
      upsertJob(await cancelJob(runningJob.id));
    } catch (cause) {
      setError(errorText(cause));
    }
  }, [runningJob, upsertJob]);

  async function install(item: ToolPackage) {
    setPluginBusy(item.definition.id);
    setError("");
    try {
      setStore(await installPlugin(item.definition.id));
      await refreshTools(true);
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setPluginBusy("");
    }
  }

  async function uninstall(item: ToolPackage) {
    setPluginBusy(item.definition.id);
    setError("");
    try {
      setStore(await uninstallPlugin(item.definition.id));
      await refreshTools(true);
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setPluginBusy("");
    }
  }

  function loadInput(job: Job) {
    setSelectedJobId(job.id);
    const tool = tools.find((item) => item.id === job.toolId);
    if (!tool) return;
    setView("tools");
    setSelectedToolId(tool.id);
    setValues({ ...schemaDefaults(tool.input), ...job.input });
  }

  function changeField(name: string, next: unknown) {
    setValues((previous) => ({ ...previous, [name]: next }));
    setError("");
  }

  function changeNested(name: string, patch: Record<string, unknown>) {
    setValues((previous) => ({
      ...previous,
      [name]: { ...((previous[name] as Record<string, unknown>) ?? {}), ...patch },
    }));
    setError("");
  }

  function openSettings() {
    setSettingsError("");
    setStoreURL(savedStoreURL);
    setSettingsOpen(true);
  }

  async function saveSettings() {
    setSettingsSaving(true);
    setSettingsError("");
    try {
      setStore(await savePluginStoreURL(storeURL));
      setSavedStoreURL(storeURL.trim());
      setStoreURL(storeURL.trim());
      setSettingsOpen(false);
    } catch (cause) {
      setSettingsError(errorText(cause));
    } finally {
      setSettingsSaving(false);
    }
  }

  async function useGiteeStore() {
    setStoreURL(GITEE_STORE_URL);
    setSettingsSaving(true);
    setSettingsError("");
    try {
      setStore(await savePluginStoreURL(GITEE_STORE_URL));
      setSavedStoreURL(GITEE_STORE_URL);
      setSettingsOpen(false);
    } catch (cause) {
      setSettingsError(errorText(cause));
    } finally {
      setSettingsSaving(false);
    }
  }

  function confirmAction() {
    const pending = pendingAction;
    if (!pending) return;
    setPendingAction(null);
    void startWithInput(pending.tool, {
      ...valuesRef.current,
      ...pending.action.input,
    });
  }

  useEffect(() => {
    function onKeydown(event: KeyboardEvent) {
      const command = event.metaKey || event.ctrlKey;
      if (command && event.key === "Enter") {
        event.preventDefault();
        run();
      }
      if (command && event.key.toLowerCase() === "k") {
        event.preventDefault();
        searchRef.current?.focus();
      }
      if (
        event.key === "Escape" &&
        document.activeElement instanceof HTMLInputElement &&
        document.activeElement.dataset.search === "true"
      ) {
        setSearch("");
        searchRef.current?.blur();
      }
    }
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  }, [run]);

  useEffect(() => {
    const off = onJobEvent((job) => upsertJob(job));
    return off;
  }, [upsertJob]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      const active = jobsRef.current.filter((job) => isActive(job.status));
      if (!active.length) return;
      void Promise.all(
        active.map(async (job) => {
          try {
            upsertJob(await getJob(job.id));
          } catch {
            // The event stream remains the primary path.
          }
        }),
      );
    }, 1200);
    return () => window.clearInterval(timer);
  }, [upsertJob]);

  useEffect(() => {
    let canceled = false;
    void (async () => {
      const loadedJobs = listJobs().then(
        (result) => {
          if (!canceled) setJobs(result);
        },
        () => undefined,
      );
      const meta = Promise.all([getAppVersion(), getPluginStoreURL()]).then(
        ([version, url]) => {
          if (canceled) return;
          setAppVersion(version);
          setSavedStoreURL(url);
          setStoreURL(url);
        },
        () => undefined,
      );
      try {
        const loaded = await listTools();
        if (canceled) return;
        toolsRef.current = loaded;
        setTools(loaded);
        if (loaded[0]) selectTool(loaded[0].id);
        else void loadStore(true);
      } catch (cause) {
        if (!canceled) setError(errorText(cause));
      }
      await Promise.allSettled([loadedJobs, meta]);
      if (!canceled) setLoading(false);
    })();
    return () => {
      canceled = true;
    };
  }, [selectTool, loadStore]);

  useEffect(() => {
    setError("");
    setFieldError(null);
  }, [selectedToolId]);

  function minimizeWindow() {
    if (isMyGo()) void currentWindow.minimize();
  }

  function toggleMaximizeWindow() {
    if (isMyGo()) void currentWindow.toggleMaximize();
  }

  function closeWindow() {
    if (isMyGo()) void currentWindow.close();
  }

  const direct = !Object.keys(selectedTool?.input.properties ?? {}).length;

  return (
    <div className="app-shell">
      <header className="app-titlebar app-drag">
        <div className="app-brand">
          <span className="app-brand-mark">OT</span>
          <span className="app-brand-name">OpenTools</span>
          <Chip color="default" variant="soft" size="sm">
            v{appVersion}
          </Chip>
        </div>

        <label className="app-command app-no-drag">
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <circle cx="11" cy="11" r="6.5" />
            <path d="m16 16 4 4" />
          </svg>
          <Input
            ref={searchRef}
            data-search="true"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索工具、动作或分类"
            aria-label="搜索工具"
            className="app-command-input"
          />
          <Kbd>Ctrl K</Kbd>
        </label>

        <div className="app-title-status">
          {activeJobs.length > 0 && (
            <span className="app-running-state">
              <span className="app-status-pulse" />
              <span>{activeJobs.length} 个任务运行中</span>
            </span>
          )}
          <div className="app-window-controls app-no-drag" aria-label="窗口控制">
            <button type="button" title="最小化" aria-label="最小化" onClick={minimizeWindow}>
              <svg viewBox="0 0 12 12" aria-hidden="true">
                <path d="M2 6h8" />
              </svg>
            </button>
            <button
              type="button"
              title="最大化或还原"
              aria-label="最大化或还原"
              onClick={toggleMaximizeWindow}
            >
              <svg viewBox="0 0 12 12" aria-hidden="true">
                <rect x="2.5" y="2.5" width="7" height="7" rx="0.5" />
              </svg>
            </button>
            <button
              type="button"
              className="close"
              title="关闭"
              aria-label="关闭"
              onClick={closeWindow}
            >
              <svg viewBox="0 0 12 12" aria-hidden="true">
                <path d="m2.5 2.5 7 7m0-7-7 7" />
              </svg>
            </button>
          </div>
        </div>
      </header>

      <div className="app-workspace">
        <aside className="app-rail">
          <div className="app-rail-head">
            <div className="app-rail-tabs">
              <button
                type="button"
                className={view === "tools" ? "active" : ""}
                onClick={() => setView("tools")}
              >
                工具
              </button>
              <button
                type="button"
                className={view === "store" ? "active" : ""}
                onClick={showStore}
              >
                商店
              </button>
            </div>
            <span className="mono app-count">
              {String(view === "tools" ? tools.length : availablePackages.length).padStart(2, "0")}
            </span>
          </div>

          {view === "tools" ? (
            <nav className="app-tool-nav" aria-label="工具">
              {categories.map(([category, items]) => (
                <section key={category} className="app-tool-group">
                  <h2>{category}</h2>
                  {items.map((tool) => (
                    <button
                      key={tool.id}
                      type="button"
                      className={`app-tool-link ${selectedToolId === tool.id ? "selected" : ""}`}
                      onClick={() => selectTool(tool.id)}
                    >
                      <span className="app-tool-glyph">
                        <ToolIcon icon={tool.icon} label={tool.name} />
                      </span>
                      <span className="app-tool-copy">
                        <span className="app-tool-name-row">
                          <span className="app-tool-name">{tool.name}</span>
                          <Chip color="default" variant="soft" size="sm">
                            v{tool.version}
                          </Chip>
                        </span>
                        <span className="app-tool-summary">{tool.summary}</span>
                      </span>
                    </button>
                  ))}
                </section>
              ))}
              {categories.length === 0 && <p className="app-rail-empty">没有匹配的工具。</p>}
            </nav>
          ) : (
            <nav className="app-tool-nav" aria-label="工具商店">
              <section className="app-tool-group">
                <h2>可获取</h2>
                {availablePackages.map((item) => (
                  <button
                    key={item.definition.id}
                    type="button"
                    className={`app-tool-link ${
                      selectedToolId === item.definition.id && item.installed ? "selected" : ""
                    }`}
                    onClick={() => item.installed && selectTool(item.definition.id)}
                  >
                    <span className="app-tool-glyph">
                      <ToolIcon icon={item.definition.icon} label={item.definition.name} />
                    </span>
                    <span className="app-tool-copy">
                      <span className="app-tool-name-row">
                        <span className="app-tool-name">{item.definition.name}</span>
                        <Chip
                          color={item.installed ? "success" : "default"}
                          variant="soft"
                          size="sm"
                        >
                          {item.installed ? "已安装" : "可下载"}
                        </Chip>
                      </span>
                      <span className="app-tool-summary">{item.definition.summary}</span>
                    </span>
                  </button>
                ))}
                {availablePackages.length === 0 && (
                  <p className="app-rail-empty">
                    {storeLoading ? "正在获取商店目录…" : "商店暂无可用工具。"}
                  </p>
                )}
              </section>
            </nav>
          )}

          <div className="app-history">
            <div className="app-rail-head">
              <span className="eyebrow">最近运行</span>
              <span className="mono app-count">
                {String(recentJobs.length).padStart(2, "0")}
              </span>
            </div>
            {recentJobs.slice(0, 12).map((job) => (
              <button
                key={job.id}
                type="button"
                className={`app-history-item ${selectedJobId === job.id ? "selected" : ""}`}
                onClick={() => loadInput(job)}
              >
                <span className={`app-history-dot ${statusDot(job.status)}`} />
                <span className="app-history-tool">
                  {tools.find((tool) => tool.id === job.toolId)?.name ?? job.toolId}
                </span>
                <span className="app-history-time mono">{shortTime(job.createdAt)}</span>
              </button>
            ))}
            {jobs.length === 0 && <p className="app-rail-empty">暂无运行记录。</p>}
          </div>

          <div className="app-rail-utility">
            <button type="button" className="app-rail-utility-button" onClick={openSettings}>
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="M12 15.25A3.25 3.25 0 1 0 12 8.75a3.25 3.25 0 0 0 0 6.5Z" />
                <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06A1.7 1.7 0 0 0 15 19.4a1.7 1.7 0 0 0-1.02 1.56V21h-4v-.04A1.7 1.7 0 0 0 8.96 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.56-1.02H3v-4h.04A1.7 1.7 0 0 0 4.6 8.96a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-1.56V3h4v.04A1.7 1.7 0 0 0 15.04 4.6a1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.18.63.74 1.03 1.56 1H21v4h-.04a1.7 1.7 0 0 0-1.56 1Z" />
              </svg>
              <span>设置</span>
            </button>
            <button
              type="button"
              className="app-rail-utility-button"
              onClick={() => applyTheme(theme === "dark" ? "light" : "dark")}
            >
              {theme === "dark" ? (
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <circle cx="12" cy="12" r="3.8" />
                  <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4" />
                </svg>
              ) : (
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M20.1 15.2A8.4 8.4 0 0 1 8.8 3.9 8.5 8.5 0 1 0 20.1 15.2Z" />
                </svg>
              )}
              <span>{theme === "dark" ? "亮色模式" : "暗色模式"}</span>
            </button>
          </div>
        </aside>

        <main className="app-workbench">
          {loading ? (
            <div className="app-loading-state">
              <span className="app-loading-line" />
              <span className="eyebrow">正在加载本地工具</span>
            </div>
          ) : view === "store" ? (
            <>
              <header className="app-store-header">
                <div>
                  <span className="eyebrow">OpenTools Store</span>
                  <h1>工具商店</h1>
                  <p>核心只提供运行框架。工具按需下载，独立更新，也可以随时卸载。</p>
                </div>
                <div className="flex flex-none gap-2">
                  <Button variant="secondary" isDisabled={storeLoading} onPress={() => loadStore(true)}>
                    {storeLoading ? "正在刷新…" : "刷新目录"}
                  </Button>
                </div>
              </header>

              {error && (
                <div className="app-banner">
                  <Chip color="danger" variant="soft" size="sm">
                    操作失败
                  </Chip>
                  <span>{error}</span>
                </div>
              )}

              {Boolean(store?.warnings?.length) && (
                <div className="app-store-warning">
                  <strong>部分目录暂不可用</strong>
                  {(store?.warnings ?? []).map((warning) => (
                    <span key={warning}>{warning}</span>
                  ))}
                </div>
              )}

              <section className="app-store-section">
                <div className="app-store-section-head">
                  <div>
                    <h2>工具目录</h2>
                  </div>
                  <span className="mono">{String(storePackages.length).padStart(2, "0")}</span>
                </div>
                {storePackages.length > 0 ? (
                  <div className="app-store-grid">
                    {storePackages.map((item) => (
                      <Card
                        key={item.definition.id}
                        className={[
                          "app-store-card",
                          item.installed ? "installed" : "",
                          !item.compatible ? "disabled" : "",
                        ]
                          .filter(Boolean)
                          .join(" ")}
                        variant="default"
                      >
                        <div className="app-store-card-head">
                          <span className="app-store-icon">
                            <ToolIcon icon={item.definition.icon} label={item.definition.name} />
                          </span>
                          <div className="app-store-card-title">
                            <div>
                              <h3>{item.definition.name}</h3>
                              <span className="mono app-store-version">
                                v{item.installedVersion || item.definition.version}
                              </span>
                            </div>
                            <p>{item.definition.summary}</p>
                            <div className="app-store-meta">
                              <span>{item.definition.category}</span>
                              <span>{item.platform || "未知平台"}</span>
                            </div>
                          </div>
                        </div>
                        <div className="app-store-card-actions">
                          {item.installed ? (
                            <>
                              {item.updateAvailable ? (
                                <Button
                                  size="sm"
                                  isDisabled={pluginBusy === item.definition.id}
                                  onPress={() => install(item)}
                                >
                                  {pluginBusy === item.definition.id ? "更新中…" : "更新"}
                                </Button>
                              ) : (
                                <Button size="sm" onPress={() => selectTool(item.definition.id)}>
                                  打开工具
                                </Button>
                              )}
                              <Button
                                variant="danger-soft"
                                size="sm"
                                isDisabled={pluginBusy === item.definition.id}
                                onPress={() => uninstall(item)}
                              >
                                {pluginBusy === item.definition.id ? "卸载中…" : "卸载"}
                              </Button>
                            </>
                          ) : (
                            <Button
                              size="sm"
                              isDisabled={pluginBusy === item.definition.id || !item.compatible}
                              onPress={() => install(item)}
                            >
                              {pluginBusy === item.definition.id
                                ? "安装中…"
                                : item.updateAvailable
                                  ? "更新"
                                  : "安装"}
                            </Button>
                          )}
                        </div>
                      </Card>
                    ))}
                  </div>
                ) : (
                  <div className="app-store-empty">
                    <span className="app-empty-index">00</span>
                    <div>
                      <h3>还没有安装工具</h3>
                      <p>联网刷新目录后选择一个工具下载，安装完成会立即出现在左侧。</p>
                    </div>
                  </div>
                )}
              </section>
            </>
          ) : selectedTool ? (
            <>
              {error && (
                <div className="app-banner">
                  <Chip color="danger" variant="soft" size="sm">
                    请求失败
                  </Chip>
                  <span>{error}</span>
                </div>
              )}

              <div className={`app-workspace-board ${direct ? "direct" : ""}`}>
                <section
                  className={`app-tool-surface ${selectedJob ? "has-result" : "no-result"}`}
                >
                  <header className="app-tool-head">
                    <div className="app-tool-inline-title">
                      <span className="app-tool-inline-icon">
                        <ToolIcon icon={selectedTool.icon} label={selectedTool.name} />
                      </span>
                      <div className="app-tool-inline-copy">
                        <div className="app-tool-inline-name">
                          <h1>{selectedTool.name}</h1>
                          <Chip color="accent" variant="soft" size="sm">
                            v{selectedTool.version}
                          </Chip>
                        </div>
                      </div>
                    </div>
                    <div className="app-tool-head-actions">
                      {runningJob ? (
                        <Button variant="danger" onPress={cancel}>
                          停止任务
                        </Button>
                      ) : (
                        <Button className="app-run-button" onPress={run}>
                          运行工具
                          <Kbd variant="light">Ctrl ↵</Kbd>
                        </Button>
                      )}
                    </div>
                  </header>
                  <ToolForm
                    tool={selectedTool}
                    model={values}
                    fieldError={fieldError}
                    onChange={changeField}
                    onNestedChange={changeNested}
                    onRun={run}
                  />
                  <ResultView job={selectedJob} tool={selectedTool} onAction={runAction} />
                </section>
              </div>
            </>
          ) : (
            <div className="app-empty-result standalone">
              <span className="app-empty-index">!</span>
              <div>
                <span className="eyebrow">暂无工具</span>
                <h2>还没有安装工具</h2>
                <p>OpenTools 核心不内置业务工具。前往工具商店下载后即可使用。</p>
                {error && <p className="app-empty-error">{error}</p>}
                <Button className="mt-4" onPress={showStore}>
                  打开工具商店
                </Button>
              </div>
            </div>
          )}
        </main>
      </div>

      <AppModal
        open={Boolean(pendingAction)}
        title={pendingAction?.action.label ?? "确认操作"}
        confirmLabel="确认执行"
        onClose={() => setPendingAction(null)}
        onConfirm={confirmAction}
      >
        <p className="app-confirm-copy">{pendingAction?.action.confirm}</p>
      </AppModal>

      <AppModal
        open={settingsOpen}
        title="设置"
        confirmLabel={settingsSaving ? "保存中…" : "保存"}
        confirmDisabled={settingsSaving}
        onClose={() => {
          if (!settingsSaving) setSettingsOpen(false);
        }}
        onConfirm={() => void saveSettings()}
      >
        <div className="app-settings-form">
          <div className="app-field">
            <div className="app-field-heading">
              <span className="app-field-label">工具商店 URL</span>
              <span className="app-settings-optional">可选</span>
            </div>
            <Input
              className="app-input"
              value={storeURL}
              onChange={(event) => setStoreURL(event.target.value)}
              placeholder="https://example.com/opentools/catalog.json"
              disabled={settingsSaving}
              aria-label="工具商店 URL"
            />
            <div className="app-settings-preset">
              <div className="app-settings-preset__label">
                <span className="app-settings-preset__name">Gitee 源</span>
                <code>{GITEE_STORE_URL}</code>
              </div>
              <Button
                variant="secondary"
                size="sm"
                isDisabled={settingsSaving}
                onPress={() => void useGiteeStore()}
              >
                使用
              </Button>
            </div>
            <span className="app-settings-hint">
              留空时使用程序自带目录（联网环境自动回退到 Gitee
              源）。多个地址可用英文分号分隔，也支持本地文件路径。
            </span>
          </div>
          {settingsError && (
            <div className="app-settings-error" role="alert">
              <Chip color="danger" variant="soft" size="sm">
                保存失败
              </Chip>
              <span>{settingsError}</span>
            </div>
          )}
          {Boolean(store?.sources?.length) && !settingsError && (
            <div className="app-settings-current">
              <span className="eyebrow">当前来源</span>
              {(store?.sources ?? []).map((source) => (
                <code key={source}>{source}</code>
              ))}
            </div>
          )}
        </div>
      </AppModal>
    </div>
  );
}
