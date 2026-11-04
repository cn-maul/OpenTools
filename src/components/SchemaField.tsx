import { useEffect, useState } from "react";

import {
  Button,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  Select,
  Switch,
  TextArea,
} from "@heroui/react";

import { chooseDirectory, chooseFile, readTextFile } from "../api";
import type { UISchema } from "../types";

type SchemaFieldProps = {
  name: string;
  schema: UISchema;
  model: Record<string, unknown>;
  required?: boolean;
  depth?: number;
  error?: string;
  fieldError?: { field: string; text: string } | null;
  onChange: (name: string, value: unknown) => void;
  onNestedChange: (name: string, patch: Record<string, unknown>) => void;
};

export function schemaDefaults(schema: UISchema): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [name, child] of Object.entries(schema.properties ?? {})) {
    if (child.default !== undefined) {
      out[name] = structuredClone(child.default);
    } else if (child.type === "boolean") {
      out[name] = false;
    } else if (child.type === "number" || child.type === "integer") {
      out[name] = 0;
    } else if (child.type === "object") {
      out[name] = schemaDefaults(child);
    } else if (child.type === "array") {
      out[name] = [];
    } else {
      out[name] = "";
    }
  }
  return out;
}

export default function SchemaField({
  name,
  schema,
  model,
  required = false,
  depth = 0,
  error,
  fieldError,
  onChange,
  onNestedChange,
}: SchemaFieldProps) {
  const value = model[name];
  const hints = schema["x-ui"];
  const widget = hints?.widget ?? "";
  const placeholder = hints?.placeholder ?? "";
  const fieldId = `field-${name}-${depth}`;
  const hidden = widget === "hidden";
  const wide = widget === "code" || widget === "textarea" || Boolean(hints?.rows);
  const enumValues = schema.enum ?? [];

  const [arrayDraft, setArrayDraft] = useState(() =>
    JSON.stringify(value ?? [], null, 2),
  );
  const [arrayError, setArrayError] = useState("");
  const [lastArrayValue, setLastArrayValue] = useState(() => JSON.stringify(value ?? []));

  useEffect(() => {
    if (schema.type !== "object" || value !== undefined) return;
    const nested = schemaDefaults(schema);
    if (Object.keys(nested).length) onChange(name, nested);
    // The nested object is initialised once for a fresh model.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schema.type, value]);

  async function pickDirectory() {
    const path = await chooseDirectory();
    if (path) onChange(name, path);
  }

  async function pickFile() {
    const path = await chooseFile(hints?.accept);
    if (!path) return;
    if (widget === "code" || hints?.rows) onChange(name, await readTextFile(path));
    else onChange(name, path);
  }

  function updateArrayDraft(text: string) {
    setArrayDraft(text);
    try {
      const parsed = JSON.parse(text);
      if (!Array.isArray(parsed)) throw new Error("必须是 JSON 数组。");
      setArrayError("");
      setLastArrayValue(JSON.stringify(parsed));
      onChange(name, parsed);
    } catch (cause) {
      setArrayError(cause instanceof Error ? cause.message : String(cause));
    }
  }

  function showArrayDraft() {
    const current = JSON.stringify(value ?? []);
    if (current !== lastArrayValue) {
      setArrayDraft(JSON.stringify(value ?? [], null, 2));
      setLastArrayValue(current);
    }
  }

  if (hidden) {
    return <input type="hidden" name={name} value={String(value ?? "")} readOnly />;
  }

  if (schema.type === "object") {
    const nested = (model[name] as Record<string, unknown> | undefined) ?? {};
    return (
      <fieldset className="app-schema-object field--wide">
        {schema.title && (
          <legend>
            {schema.title}
            {required && <span className="app-required">必填</span>}
          </legend>
        )}
        <div className="app-schema-object-grid">
          {Object.entries(schema.properties ?? {}).map(([childName, child]) => (
            <SchemaField
              key={childName}
              name={childName}
              schema={child}
              model={nested}
              required={schema.required?.includes(childName)}
              depth={depth + 1}
              fieldError={fieldError}
              onChange={(key, next) => onNestedChange(childName, { [key]: next })}
              onNestedChange={(key, patch) =>
                onNestedChange(childName, {
                  [key]: { ...((nested[key] as Record<string, unknown>) ?? {}), ...patch },
                })
              }
            />
          ))}
        </div>
      </fieldset>
    );
  }

  const label = schema.title || name;
  const textValue = String(value ?? "");

  return (
    <div
      className={[
        "field app-field",
        wide ? "field--wide" : "",
        widget === "directory" || widget === "file" ? "field--path" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
      <div className="app-field-heading">
        <Label htmlFor={fieldId} className="app-field-label">
          {label}
        </Label>
        {required && <span className="app-required">必填</span>}
      </div>

      {schema.type === "boolean" ? (
        <div className="app-switch-row">
          <Switch size="sm" isSelected={Boolean(value)} onChange={(checked) => onChange(name, checked)}>
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
          <span className="app-switch-label">{value ? "开启" : "关闭"}</span>
        </div>
      ) : enumValues.length ? (
        <Select
          id={fieldId}
          className="app-select"
          aria-label={label}
          selectedKey={textValue}
          onSelectionChange={(key) => onChange(name, String(key ?? ""))}
        >
          <Select.Trigger>
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox aria-label={label}>
              {enumValues.map((option) => {
                const optionValue = String(option);
                const optionLabel = hints?.enumLabels?.[optionValue] ?? optionValue;
                return (
                  <ListBoxItem key={optionValue} id={optionValue} textValue={optionLabel}>
                    {optionLabel}
                  </ListBoxItem>
                );
              })}
            </ListBox>
          </Select.Popover>
        </Select>
      ) : widget === "directory" || widget === "file" ? (
        <div className="app-path-field">
          <Input
            id={fieldId}
            className="app-input"
            value={textValue}
            onChange={(event) => onChange(name, event.target.value)}
            placeholder={
              placeholder || (widget === "directory" ? "选择一个文件夹" : "选择一个文件")
            }
          />
          <Button
            variant="secondary"
            onPress={() => (widget === "directory" ? pickDirectory() : pickFile())}
          >
            选择
          </Button>
        </div>
      ) : widget === "code" ? (
        <div className="app-code-input">
          <div className="app-code-input-head">
            <span>JSON 源码</span>
            <Button variant="secondary" size="sm" onPress={pickFile}>
              从文件载入
            </Button>
          </div>
          <TextArea
            id={fieldId}
            className="app-code-input__area"
            value={textValue}
            onChange={(event) => onChange(name, event.target.value)}
            placeholder={placeholder}
            rows={hints?.rows ?? 12}
          />
        </div>
      ) : widget === "textarea" || hints?.rows ? (
        <TextArea
          id={fieldId}
          className="app-textarea"
          value={textValue}
          onChange={(event) => onChange(name, event.target.value)}
          placeholder={placeholder}
          rows={hints?.rows ?? 6}
        />
      ) : schema.type === "number" || schema.type === "integer" ? (
        <Input
          id={fieldId}
          type="number"
          className="app-input"
          value={value === "" || value === undefined ? "" : String(value)}
          onChange={(event) =>
            onChange(name, event.target.value === "" ? 0 : Number(event.target.value))
          }
          min={schema.minimum}
          max={schema.maximum}
          step={schema.type === "integer" ? 1 : "any"}
        />
      ) : schema.type === "array" ? (
        <Input
          id={fieldId}
          className="app-input mono"
          value={arrayDraft}
          onChange={(event) => updateArrayDraft(event.target.value)}
          onFocus={showArrayDraft}
          placeholder={placeholder || "[]"}
        />
      ) : (
        <Input
          id={fieldId}
          className="app-input"
          type={hints?.secret ? "password" : "text"}
          value={textValue}
          onChange={(event) => onChange(name, event.target.value)}
          placeholder={placeholder}
          minLength={schema.minLength}
          maxLength={schema.maxLength}
        />
      )}

      {arrayError && <p className="app-field-error">请输入有效的 JSON 数组。</p>}
      {error && <p className="app-field-error">{error}</p>}
    </div>
  );
}
