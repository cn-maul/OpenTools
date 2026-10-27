import { Jobs, events } from "./mygo";
import type { Job, Result, ToolDefinition, ToolStore } from "./types";

export async function listTools(): Promise<ToolDefinition[]> {
  return (await Jobs.tools()) as unknown as ToolDefinition[];
}

export async function getAppVersion(): Promise<string> {
  return await Jobs.appVersion();
}

export async function getPluginStore(): Promise<ToolStore> {
  return (await Jobs.pluginStore()) as unknown as ToolStore;
}

export async function getPluginStoreURL(): Promise<string> {
  return await Jobs.pluginStoreURL();
}

export async function savePluginStoreURL(value: string): Promise<ToolStore> {
  return (await Jobs.savePluginStoreURL(value)) as unknown as ToolStore;
}

export async function refreshPluginStore(): Promise<ToolStore> {
  return (await Jobs.refreshPluginStore()) as unknown as ToolStore;
}

export async function installPlugin(id: string): Promise<ToolStore> {
  return (await Jobs.installPlugin(id)) as unknown as ToolStore;
}

export async function uninstallPlugin(id: string): Promise<ToolStore> {
  return (await Jobs.uninstallPlugin(id)) as unknown as ToolStore;
}

export async function startJob(
  toolId: string,
  input: Record<string, unknown>,
): Promise<Job> {
  return (await Jobs.start(toolId, input)) as unknown as Job;
}

export async function getJob(id: string): Promise<Job> {
  return (await Jobs.get(id)) as unknown as Job;
}

export async function listJobs(limit = 40): Promise<Job[]> {
  return (await Jobs.list(limit)) as unknown as Job[];
}

export async function cancelJob(id: string): Promise<Job> {
  return (await Jobs.cancel(id)) as unknown as Job;
}

export function onJobEvent(handler: (job: Job) => void): () => void {
  return events.jobsChanged.on((event) => handler(event.job as unknown as Job));
}

export async function chooseDirectory(): Promise<string> {
  return await Jobs.chooseDirectory();
}

export async function chooseFile(extensions: string[] = []): Promise<string> {
  const filters = extensions.length
    ? [{ description: extensions.map((ext) => ext.toUpperCase()).join(", "), extensions }]
    : [];
  return await Jobs.chooseFile(filters);
}

export async function readTextFile(path: string): Promise<string> {
  return await Jobs.readTextFile(path);
}

export function parseFieldError(message: string): { field: string; text: string } | null {
  const match = /^([a-z_]+)\|([^|]+)\|(.+)$/.exec(message);
  if (!match) return null;
  return { field: match[2] ?? "", text: match[3] ?? message };
}

export function resultText(result: Result): string {
  const parts = [result.summary];
  for (const block of result.blocks ?? []) {
    if (block.title) parts.push("", block.title);
    if (block.text) parts.push(block.text);
    if (block.kind === "json") parts.push(JSON.stringify(block.data, null, 2));
    if (block.kind === "table") {
      if (block.columns) parts.push(block.columns.join("\t"));
      for (const row of block.rows ?? []) parts.push(row.join("\t"));
    }
  }
  for (const block of result.blocks ?? []) {
    if (block.kind !== "status-grid") continue;
    const data = block.data as { cells?: { label: string; detail?: string }[] } | undefined;
    if (!Array.isArray(data?.cells)) continue;
    parts.push("", block.title ?? "状态网格");
    for (const cell of data.cells) {
      parts.push(cell.detail ? `${cell.label}  ${cell.detail}` : cell.label);
    }
  }
  return parts.join("\n");
}
