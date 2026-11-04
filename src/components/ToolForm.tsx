import type { ToolDefinition } from "../types";
import SchemaField from "./SchemaField";

type ToolFormProps = {
  tool: ToolDefinition;
  model: Record<string, unknown>;
  fieldError?: { field: string; text: string } | null;
  onChange: (name: string, value: unknown) => void;
  onNestedChange: (name: string, patch: Record<string, unknown>) => void;
  onRun: () => void;
};

export default function ToolForm({
  tool,
  model,
  fieldError,
  onChange,
  onNestedChange,
  onRun,
}: ToolFormProps) {
  const fields = Object.entries(tool.input.properties ?? {}).filter(([, schema]) => {
    const condition = schema["x-ui"]?.visibleWhen;
    if (!condition) return true;
    const current = model[condition.field];
    if (condition.in?.length) return condition.in.includes(String(current));
    if (condition.equals !== undefined) return current === condition.equals;
    return true;
  });
  const required = new Set(tool.input.required ?? []);

  if (fields.length === 0) return null;

  return (
    <form
      className="app-tool-form"
      onSubmit={(event) => {
        event.preventDefault();
        onRun();
      }}
    >
      <div className="app-form-grid">
        {fields.map(([name, schema]) => (
          <SchemaField
            key={name}
            name={name}
            schema={schema}
            model={model}
            required={required.has(name)}
            error={fieldError?.field === name ? fieldError.text : ""}
            fieldError={fieldError}
            onChange={onChange}
            onNestedChange={onNestedChange}
          />
        ))}
      </div>
    </form>
  );
}
