export type BlockKind =
  | "text"
  | "notice"
  | "code"
  | "json"
  | "table"
    | "diff"
    | "status-grid";

  export interface StatusCell {
    label: string;
    tone?: Tone;
    category?: string;
    detail?: string;
  }

  export interface StatusLegendItem {
    label: string;
    tone?: Tone;
    category?: string;
  }

export type Tone = "neutral" | "success" | "warning" | "danger";

export interface Block {
  kind: BlockKind;
  title?: string;
  tone?: Tone;
  text?: string;
  language?: string;
  data?: unknown;
  columns?: string[];
  rows?: string[][];
}

export interface Artifact {
  name: string;
  path?: string;
  mime?: string;
}

export interface Result {
  summary: string;
  blocks?: Block[];
  artifacts?: Artifact[];
  actions?: ResultAction[];
  warnings?: string[];
}

export interface ResultAction {
  label: string;
  tone?: Tone;
  input: Record<string, unknown>;
  confirm?: string;
}

export interface UISchema {
  type?: "object" | "string" | "number" | "integer" | "boolean" | "array";
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  pattern?: string;
  properties?: Record<string, UISchema>;
  required?: string[];
  items?: UISchema;
  "x-ui"?: {
    widget?: string;
    placeholder?: string;
    group?: string;
    rows?: number;
    secret?: boolean;
    accept?: string[];
    enumLabels?: Record<string, string>;
    visibleWhen?: {
      field: string;
      equals?: unknown;
      in?: string[];
    };
  };
}

export interface ToolDefinition {
  id: string;
  name: string;
  summary: string;
  description?: string;
  category: string;
  icon?: string;
  keywords?: string[];
  version: string;
  effects?: string[];
  input: UISchema;
}

export interface ToolPackage {
  definition: ToolDefinition;
  platform: string;
  protocolVersion: number;
  author?: string;
  homepage?: string;
  license?: string;
  downloadUrl: string;
  sha256: string;
  size?: number;
  compatible: boolean;
  installed: boolean;
  installedVersion?: string;
  updateAvailable: boolean;
}

export interface ToolStore {
  packages: ToolPackage[];
  sources: string[];
  warnings?: string[];
  fetchedAt: string;
}

export type SectionState = "pending" | "running" | "ready" | "skipped";

export interface ResultSection {
  id: string;
  title: string;
  state: SectionState;
  text?: string;
  blocks?: Block[];
}

export type JobStatus =
  | "queued"
  | "running"
  | "canceling"
  | "succeeded"
  | "failed"
  | "canceled";

export interface LogEntry {
  at: string;
  level: string;
  message: string;
}

export interface Job {
  id: string;
  toolId: string;
  revision: number;
  status: JobStatus;
  input: Record<string, unknown>;
  progress: { done: number; total: number };
  logs?: LogEntry[];
  sections?: ResultSection[];
  result?: Result;
  error?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
}
