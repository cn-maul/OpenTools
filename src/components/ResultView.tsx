import { useEffect, useMemo, useRef, useState } from "react";

import { Alert, Button, Chip, Kbd } from "@heroui/react";

import { copyArtifactPath, resultText, revealArtifact } from "../api";
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
import MetricView from "./MetricView";
import ResultTable from "./ResultTable";
import StatusGrid from "./StatusGrid";

type ResultViewProps = {
  job?: Job;
  tool?: ToolDefinition;
  onAction: (action: ResultAction) => void;
};

const TABBED_TOOLS = new Set(["system.crash-diagnostics"]);

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

function tone(value?: string): "default" | "accent" | "success" | "warning" | "danger" {
  if (value === "success") return "success";
  if (value === "warning") return "warning";
  if (value === "danger") return "danger";
  if (value === "accent") return "accent";
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

function blockItems<T>(block: Block): T[] {
  if (Array.isArray(block.data)) return block.data as T[];
  const data = block.data as { items?: unknown } | undefined;
  return Array.isArray(data?.items) ? (data.items as T[]) : [];
}

function meterFacts(block: Block): FactItem[] {
  const data = block.data as { facts?: unknown } | undefined;
  return Array.isArray(data?.facts) ? (data.facts as FactItem[]) : [];
}

function gridCells(block: Block): StatusCell[] {
  const data = block.data as { cells?: unknown } | undefined;
  return Array.isArray(data?.cells) ? (data.cells as StatusCell[]) : [];
}

function gridLegend(block: Block): StatusLegendItem[] {
  const data = block.data as { legend?: unknown } | undefined;
  return Array.isArray(data?.legend) ? (data.legend as StatusLegendItem[]) : [];
}

function gridLabeled(block: Block): boolean {
  const data = block.data as { labeled?: unknown } | undefined;
  return data?.labeled === true;
}

function shortSectionTitle(toolID: string | undefined, section: ResultSection) {
  return (
    {
      overview: "概览",
      pressure: "资源压力",
      processes: "占用排行",
      disks: "分区空间",
      bugchecks: "蓝屏",
      unexpected: "异常关机",
      dumps: "转储文件",
      registry: "注册表",
      tasks: "计划任务",
      services: "服务",
    }[section.id] ?? section.title
  );
}

function sectionRows(section: ResultSection) {
  let count = 0;
  for (const block of section.blocks ?? []) {
    if (block.kind === "table") count += block.rows?.length ?? 0;
  }
  return count;
}

function isLightSection(section: ResultSection) {
  const blocks = section.blocks ?? [];
  if (blocks.length === 0) return true;
  if (blocks.length > 1) return false;
  const block = blocks[0] as Block | undefined;
  if (!block) return true;
  if (block.kind === "notice") return true;
  if (block.kind === "facts") {
    const items = blockItems<FactItem>(block);
    const groups = new Set(items.map((item) => item.group ?? ""));
    if (items.length === 0 || items.length > 4 || groups.size > 1) return false;
    return !items.some((item) => item.value.length > 24);
  }
  if (block.kind === "meter") {
    const items = blockItems<MeterItem>(block);
    if (items.some((item) => item.variant === "bar")) return false;
    return items.length <= 2 && meterFacts(block).length <= 2;
  }
  return false;
}

function artifactMimeLabel(mime?: string) {
  if (!mime) return "文件";
  if (mime.includes("wordprocessingml")) return "Word 文档";
  if (mime.includes("spreadsheetml")) return "Excel 表格";
  if (mime.includes("presentationml")) return "演示文稿";
  if (mime === "text/csv") return "CSV 表格";
  if (mime.startsWith("text/")) return "文本文件";
  if (mime.startsWith("image/")) return "图片";
  if (mime === "application/pdf") return "PDF 文档";
  return "文件";
}

function ArtifactsView({ job }: { job: Job }) {
  const [busy, setBusy] = useState("");
  const artifacts = job.result?.artifacts ?? [];
  if (artifacts.length === 0) return null;

  async function run(key: string, action: () => Promise<void>) {
    setBusy(key);
    try {
      await action();
    } finally {
      setBusy((current) => (current === key ? "" : current));
    }
  }

  return (
    <div className="app-result-artifacts">
      <span className="app-result-label">产出文件</span>
      {artifacts.map((artifact) => {
        const key = `${job.id}:${artifact.name}`;
        return (
          <div key={artifact.name} className="app-artifact-row">
            <div className="app-artifact-info">
              <span className="app-artifact-name">{artifact.name}</span>
              <span className="app-artifact-kind">{artifactMimeLabel(artifact.mime)}</span>
            </div>
            <div className="app-artifact-actions">
              <Button
                variant="secondary"
                size="sm"
                isDisabled={busy === key}
                onPress={() => run(key, () => revealArtifact(job.id, artifact.name))}
              >
                {busy === key ? "打开中…" : "打开文件"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                isDisabled={busy === key}
                onPress={() => run(key, () => copyArtifactPath(job.id, artifact.name))}
              >
                {busy === key ? "已复制" : "复制路径"}
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
}

export default function ResultView({ job, tool, onAction }: ResultViewProps) {
  const [copied, setCopied] = useState("");
  const [logsOpen, setLogsOpen] = useState(false);
  const [selectedSectionID, setSelectedSectionID] = useState("");
  const contentRef = useRef<HTMLDivElement>(null);

  const sections = useMemo(() => job?.sections ?? [], [job?.sections]);
  const stacked = sections.length > 2;
  const tabbed = Boolean(tool?.id && TABBED_TOOLS.has(tool.id)) && sections.length > 1;
  const shownSections = tabbed
    ? sections.filter((section) => section.id === selectedSectionID)
    : sections;
  const singleTable =
    sections.length === 1 &&
    (sections[0]?.blocks?.length ?? 0) === 1 &&
    sections[0]?.blocks?.[0]?.kind === "table";
  const progress = (() => {
    if (!job) return 0;
    const { done, total } = job.progress;
    return total > 0 ? Math.min(100, (done / total) * 100) : 0;
  })();

  const elapsed = (() => {
    if (!job?.startedAt) return "";
    const end = job.finishedAt ? new Date(job.finishedAt) : new Date();
    const ms = end.getTime() - new Date(job.startedAt).getTime();
    if (ms < 1000) return `${Math.max(0, ms)} ms`;
    return `${(ms / 1000).toFixed(1)} s`;
  })();

  function defaultSectionID(list: ResultSection[]) {
    return list[0]?.id ?? "";
  }

  const key = `${job?.id ?? ""}:${sections.map((section) => `${section.id}:${section.state}`).join("|")}`;
  const previousKey = useRef("");

  useEffect(() => {
    if (previousKey.current === key) return;
    previousKey.current = key;
    setSelectedSectionID((current) =>
      sections.some((section) => section.id === current)
        ? current
        : defaultSectionID(sections),
    );
  }, [key, sections]);

  useEffect(() => {
    if (job?.status === "running" || job?.status === "succeeded") {
      contentRef.current?.scrollTo({ top: 0 });
    }
  }, [job?.id, job?.status]);

  async function copy(text: string, copiedKey: string) {
    await navigator.clipboard.writeText(text);
    setCopied(copiedKey);
    window.setTimeout(() => {
      setCopied((current) => (current === copiedKey ? "" : current));
    }, 1400);
  }

  async function exportResult(current: Job) {
    if (!current.result) return;
    const blob = new Blob([resultText(current.result)], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `${current.toolId}-${current.id}.txt`;
    link.click();
    URL.revokeObjectURL(url);
    setCopied("export");
    window.setTimeout(() => setCopied(""), 1400);
  }

  function json(block: Block) {
    return JSON.stringify(block.data, null, 2);
  }

  function renderBlock(block: Block, index: number) {
    switch (block.kind) {
      case "notice":
        return (
          <Alert key={index} status={tone(block.tone)}>
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Description>{block.text}</Alert.Description>
            </Alert.Content>
          </Alert>
        );
      case "status-grid":
        return (
          <StatusGrid
            key={index}
            cells={gridCells(block)}
            legend={gridLegend(block)}
            label={block.title}
            labeled={gridLabeled(block)}
          />
        );
      case "meter":
        return (
          <MetricView
            key={index}
            metrics={blockItems<MeterItem>(block)}
            facts={meterFacts(block)}
            label={block.title}
          />
        );
      case "facts":
        return (
          <MetricView key={index} facts={blockItems<FactItem>(block)} label={block.title} />
        );
      case "text":
        return <p key={index} className="app-block-text">{block.text}</p>;
      case "code":
      case "json":
      case "diff":
        return (
          <pre
            key={index}
            className={`app-code-block ${block.language === "text" ? "inline-list" : ""}`}
          >
            <code>{block.kind === "json" ? json(block) : block.text}</code>
          </pre>
        );
      case "table":
        return (
          <ResultTable
            key={index}
            columns={block.columns ?? []}
            columnWidths={block.columnWidths}
            rows={block.rows ?? []}
            rowIcons={block.rowIcons}
            rowActions={block.rowActions}
            batchAction={block.batchAction}
            wide={(block.columns?.length ?? 0) > 5}
            label={block.title}
            onAction={onAction}
          />
        );
      default:
        return null;
    }
  }

  const toolActions = job?.result?.actions ?? [];

  return (
    <div className="app-result-pane">
      {!job ? (
        <div className="app-result-scroll">
          <div className="app-result-empty">
            <span className="app-result-empty-mark">
              <svg viewBox="0 0 32 32" aria-hidden="true">
                <path d="M7 10.5h18M7 16h11M7 21.5h6" />
                <circle cx="24" cy="21.5" r="3.5" />
              </svg>
            </span>
            <div>
              <Chip color="default" variant="soft" size="sm">
                等待运行
              </Chip>
              <h2>结果会显示在这里</h2>
              <p>配置好输入后运行工具，输出、日志与可执行操作会集中呈现在这个面板。</p>
              <div className="app-command-hint">
                <Kbd>Ctrl + Enter</Kbd>
                <span>运行当前工具</span>
              </div>
            </div>
          </div>
        </div>
      ) : (
        <>
          {(
            <div className="app-result-status">
              <span className="app-result-job-id mono">{job.id}</span>
              <div className="app-result-head-status">
                <Chip color={statusColor(job.status)} variant="soft" size="sm">
                  {statusLabel(job.status)}
                </Chip>
                {elapsed && <span className="app-result-meta mono">{elapsed}</span>}
              </div>
            </div>
          )}
          {(job.status === "running" ||
            job.status === "canceling" ||
            job.status === "queued") && (
            <div
              className={`app-progress-track ${job.progress.total === 0 ? "indeterminate" : ""}`}
              role="progressbar"
              aria-valuenow={Math.round(progress)}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <span style={{ width: `${progress}%` }} />
            </div>
          )}

          <div
            className={[
              "app-result-content",
              sections.length ? "streamlined" : "",
              stacked && !tabbed ? "stacked" : "",
              tabbed ? "tabbed" : "",
              sections.length === 1 ? "single" : "",
              singleTable ? "table-only" : "",
            ]
              .filter(Boolean)
              .join(" ")}
            ref={contentRef}
          >
            {sections.length > 0 ? (
              <>
                {job.result && (
                  <p className="app-result-summary compact">{job.result.summary}</p>
                )}

                {tabbed && (
                  <div className="app-result-tabs" role="tablist" aria-label="结果分类">
                    {sections.map((section) => (
                      <button
                        key={section.id}
                        type="button"
                        role="tab"
                        className={[
                          "app-result-tab",
                          `state-${section.state}`,
                          selectedSectionID === section.id ? "active" : "",
                        ]
                          .filter(Boolean)
                          .join(" ")}
                        aria-selected={selectedSectionID === section.id}
                        onClick={() => setSelectedSectionID(section.id)}
                      >
                        <span className="app-result-tab-state" />
                        <span>{shortSectionTitle(tool?.id, section)}</span>
                        {sectionRows(section) > 0 && (
                          <span className="app-result-tab-count">{sectionRows(section)}</span>
                        )}
                      </button>
                    ))}
                  </div>
                )}

                <div className="app-stream">
                  {shownSections.map((section, index) => (
                    <section
                      key={section.id}
                      className={[
                        "app-stream-item",
                        `state-${section.state}`,
                        stacked && !tabbed && isLightSection(section) ? "light" : "",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                    >
                      {!tabbed && (
                        <header className="app-stream-head">
                          <div className="flex min-w-0 items-center gap-2">
                            <span className="app-stream-index">
                              {String(index + 1).padStart(2, "0")}
                            </span>
                            <h3>{section.title}</h3>
                          </div>
                          <span className={`app-stream-state ${section.state}`}>
                            {section.state === "running" ? (
                              <span className="app-section-spinner" />
                            ) : (
                              <span className="app-section-dot" />
                            )}
                            {sectionStateLabel(section.state)}
                          </span>
                        </header>
                      )}

                      {section.state === "pending" ? (
                        <div className="app-stream-placeholder" aria-hidden="true">
                          <span />
                        </div>
                      ) : (
                        <>
                          {section.text && <p className="app-stream-text">{section.text}</p>}
                          {(section.blocks ?? []).map((block, blockIndex) => (
                            <div key={blockIndex} className="app-result-block">
                              {block.title && block.title !== section.title && (
                                <div className="app-block-title">
                                  <span>{block.title}</span>
                                </div>
                              )}
                              {renderBlock(block, blockIndex)}
                            </div>
                          ))}
                        </>
                      )}
                    </section>
                  ))}
                </div>

                {Boolean(job.result?.warnings?.length) && (
                  <div className="app-result-warnings">
                    {(job.result?.warnings ?? []).map((warning) => (
                      <Alert key={warning} status="warning">
                        <Alert.Indicator />
                        <Alert.Content>
                          <Alert.Description>{warning}</Alert.Description>
                        </Alert.Content>
                      </Alert>
                    ))}
                  </div>
                )}

                <ArtifactsView job={job} />

                {toolActions.length > 0 && (
                  <div className="app-result-actions">
                    <span className="app-result-label">下一步</span>
                    <div className="app-action-grid">
                      {toolActions.map((action, index) => (
                        <Button
                          key={`${action.label}-${index}`}
                          variant={
                            action.tone === "danger"
                              ? "danger-soft"
                              : action.tone === "success"
                                ? "secondary"
                                : "outline"
                          }
                          size="sm"
                          onPress={() => onAction(action)}
                        >
                          {action.label}
                        </Button>
                      ))}
                    </div>
                  </div>
                )}

                {job.result && (
                  <div className="app-result-export">
                    <Button variant="ghost" size="sm" onPress={() => exportResult(job)}>
                      {copied === "export" ? "已导出" : "导出结果"}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      onPress={() => copy(resultText(job.result!), "all")}
                    >
                      {copied === "all" ? "已复制" : "复制全部结果"}
                    </Button>
                  </div>
                )}
              </>
            ) : job.result ? (
              <>
                <p className="app-result-summary">{job.result.summary}</p>
                {(job.result.blocks ?? []).map((block, index) => (
                  <div key={index} className="app-result-block">
                    {block.title && (
                      <div className="app-block-title">
                        <span>{block.title}</span>
                        {(block.kind === "code" || block.kind === "json") && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onPress={() =>
                              copy(
                                block.kind === "json" ? json(block) : (block.text ?? ""),
                                String(index),
                              )
                            }
                          >
                            {copied === String(index) ? "已复制" : "复制"}
                          </Button>
                        )}
                      </div>
                    )}
                    {renderBlock(block, index)}
                  </div>
                ))}

                {Boolean(job.result.warnings?.length) && (
                  <div className="app-result-warnings">
                    {(job.result.warnings ?? []).map((warning) => (
                      <Alert key={warning} status="warning">
                        <Alert.Indicator />
                        <Alert.Content>
                          <Alert.Description>{warning}</Alert.Description>
                        </Alert.Content>
                      </Alert>
                    ))}
                  </div>
                )}

                <ArtifactsView job={job} />

                {toolActions.length > 0 && (
                  <div className="app-result-actions">
                    <span className="app-result-label">下一步</span>
                    <div className="app-action-grid">
                      {toolActions.map((action, index) => (
                        <Button
                          key={`${action.label}-${index}`}
                          variant={
                            action.tone === "danger"
                              ? "danger-soft"
                              : action.tone === "success"
                                ? "secondary"
                                : "outline"
                          }
                          size="sm"
                          onPress={() => onAction(action)}
                        >
                          {action.label}
                        </Button>
                      ))}
                    </div>
                  </div>
                )}

                <div className="app-result-export">
                  <Button variant="ghost" size="sm" onPress={() => exportResult(job)}>
                    {copied === "export" ? "已导出" : "导出结果"}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onPress={() =>
                      copy(resultText(job.result!), "all")
                    }
                  >
                    {copied === "all" ? "已复制" : "复制全部结果"}
                  </Button>
                </div>
              </>
            ) : (
              <div className="app-result-empty">
                <span className="app-result-empty-mark">
                  <svg viewBox="0 0 32 32" aria-hidden="true">
                    <path d="M7 10.5h18M7 16h11M7 21.5h6" />
                    <circle cx="24" cy="21.5" r="3.5" />
                  </svg>
                </span>
                <div>
                  <Chip color="default" variant="soft" size="sm">
                    运行中
                  </Chip>
                  <h2>正在准备结果</h2>
                  <p>工具正在执行，结构化输出会随着检测进度逐块出现。</p>
                </div>
              </div>
            )}
          </div>

          {(job.error || Boolean(job.logs?.length)) && (
            <div className="app-result-tail">
              {job.error && (
                <Alert status="danger">
                  <Alert.Indicator />
                  <Alert.Content>
                    <Alert.Title>执行失败</Alert.Title>
                    <Alert.Description>{job.error}</Alert.Description>
                  </Alert.Content>
                </Alert>
              )}

              {Boolean(job.logs?.length) && (
                <div className={`app-job-log ${logsOpen ? "open" : ""}`}>
                  <button
                    type="button"
                    className="app-job-log-toggle"
                    onClick={() => setLogsOpen((open) => !open)}
                  >
                    <span>活动记录</span>
                    <span className="flex items-center gap-1.5 text-muted">
                      <span className="mono text-[11.5px]">{job.logs?.length} 行</span>
                      <svg viewBox="0 0 12 12" aria-hidden="true">
                        <path d="m3 4.5 3 3 3-3" />
                      </svg>
                    </span>
                  </button>
                  <pre>
                    <code>
                      {(job.logs ?? []).map((entry, index) => (
                        <span key={index} className={`app-log-line ${entry.level}`}>
                          {new Date(entry.at).toLocaleTimeString("zh-CN", { hour12: false })}{" "}
                          {entry.message}
                          {"\n"}
                        </span>
                      ))}
                    </code>
                  </pre>
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
