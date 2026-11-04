import { useState } from "react";

import { Button, Checkbox, Switch } from "@heroui/react";

import type { BatchAction, ResultAction, TableRowAction } from "../types";

type ResultTableProps = {
  columns: string[];
  columnWidths?: string[];
  rows: string[][];
  rowIcons?: string[];
  label?: string;
  wide?: boolean;
  rowActions?: TableRowAction[][];
  batchAction?: BatchAction;
  onAction: (action: ResultAction) => void;
};

function avatarInitial(name: string) {
  return [...name.trim()][0]?.toUpperCase() ?? "?";
}

function truncateColumn(column: string) {
  return column === "命令" || column === "路径" || column === "文件名";
}

function actionTone(tone?: string) {
  if (tone === "danger") return "danger-soft" as const;
  if (tone === "success") return "secondary" as const;
  return "outline" as const;
}

export default function ResultTable({
  columns,
  columnWidths,
  rows,
  rowIcons,
  label,
  wide = false,
  rowActions,
  batchAction,
  onAction,
}: ResultTableProps) {
  const [selected, setSelected] = useState<Set<number>>(() => new Set());

  const batched = Boolean(batchAction && rowActions?.length);
  const hasActions = (rowActions?.length ?? 0) > 0 && !batched;
  const selectedIndexes = [...selected].sort((left, right) => left - right);
  const allSelected = rows.length > 0 && selectedIndexes.length === rows.length;
  const someSelected = selectedIndexes.length > 0 && !allSelected;

  const actionsHeader = rowActions?.some((row) =>
    row?.some((action) => action.variant === "select"),
  )
    ? "启动类型"
    : rowActions?.some((row) => row?.some((action) => action.variant === "switch"))
      ? "状态"
      : "操作";

  function batchItems() {
    const key = batchAction?.itemKey ?? "item";
    return selectedIndexes
      .map((index) => (rowActions?.[index]?.[0]?.input ?? {})[key])
      .filter((value) => value !== undefined && value !== null && value !== "");
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? new Set(rows.map((_, index) => index)) : new Set());
  }

  function toggleRow(index: number, checked: boolean) {
    setSelected((previous) => {
      const next = new Set(previous);
      if (checked) next.add(index);
      else next.delete(index);
      return next;
    });
  }

  function runBatch() {
    if (!batchAction) return;
    const items = batchItems();
    if (!items.length) return;
    onAction({
      label: batchAction.label,
      tone: batchAction.tone,
      input: {
        ...batchAction.input,
        [batchAction.itemsKey ?? "items"]: JSON.stringify(items),
      },
      confirm: batchAction.confirm?.replace("{count}", String(items.length)),
    });
  }

  const columnCount = columns.length + (batched ? 1 : 0) + (hasActions ? 1 : 0);

  return (
    <div className="table-root table-root--primary">
      <div className="table__scroll-container" role="region" aria-label={label} tabIndex={0}>
        <table
          className="table__content"
          aria-label={label}
          style={wide ? undefined : { maxWidth: "100%" }}
        >
          <colgroup>
            {batched && <col style={{ width: 32 }} />}
            {columns.map((column, index) => (
              <col key={`col-${column}-${index}`} style={{ width: columnWidths?.[index] }} />
            ))}
            {hasActions && <col style={{ width: batched ? undefined : "14%" }} />}
          </colgroup>
          <thead className="table__header">
            <tr>
              {batched && (
                <th className="table__column table__column--select" scope="col">
                  <Checkbox
                    aria-label={`全选 ${rows.length} 行`}
                    isSelected={allSelected}
                    isIndeterminate={someSelected}
                    onChange={toggleAll}
                  >
                    <Checkbox.Content>
                      <Checkbox.Control>
                        <Checkbox.Indicator />
                      </Checkbox.Control>
                    </Checkbox.Content>
                  </Checkbox>
                </th>
              )}
              {columns.map((column, index) => (
                <th key={`${column}-${index}`} className="table__column" scope="col">
                  {column}
                </th>
              ))}
              {hasActions && (
                <th className="table__column" scope="col">
                  {actionsHeader}
                </th>
              )}
            </tr>
          </thead>
          <tbody className="table__body">
            {rows.map((row, rowIndex) => (
              <tr key={rowIndex} className="table__row">
                {batched && (
                  <td className="table__cell table__cell--select">
                    <Checkbox
                      aria-label={`选择第 ${rowIndex + 1} 行`}
                      isSelected={selected.has(rowIndex)}
                      onChange={(checked) => toggleRow(rowIndex, checked)}
                    >
                      <Checkbox.Content>
                        <Checkbox.Control>
                          <Checkbox.Indicator />
                        </Checkbox.Control>
                      </Checkbox.Content>
                    </Checkbox>
                  </td>
                )}
                {row.map((cell, cellIndex) => {
                  const truncate = truncateColumn(columns[cellIndex] ?? "");
                  return (
                    <td key={cellIndex} className="table__cell mono">
                      {cellIndex === 0 && rowIcons?.length ? (
                        <span className="table__cell-with-icon">
                          <span className="table__row-icon" aria-hidden="true">
                            {rowIcons[rowIndex] ? (
                              <img src={rowIcons[rowIndex]} alt="" />
                            ) : (
                              avatarInitial(cell)
                            )}
                          </span>
                          <span
                            className={`table__cell-text ${truncate ? "table__cell-text--truncate" : ""}`}
                            title={cell}
                          >
                            {cell}
                          </span>
                        </span>
                      ) : (
                        <span
                          className={`table__cell-text ${truncate ? "table__cell-text--truncate" : ""}`}
                          title={cell}
                        >
                          {cell}
                        </span>
                      )}
                    </td>
                  );
                })}
                {hasActions && (
                  <td className="table__cell table__cell--actions">
                    {(rowActions?.[rowIndex] ?? []).map((action, actionIndex) => {
                      if (action.variant === "switch") {
                        return (
                          <Switch
                            key={actionIndex}
                            size="sm"
                            isSelected={Boolean(action.on)}
                            aria-label={action.label}
                            onChange={() => onAction(action)}
                          >
                            <Switch.Content>
                              <Switch.Control>
                                <Switch.Thumb />
                              </Switch.Control>
                            </Switch.Content>
                          </Switch>
                        );
                      }
                      if (action.variant === "select") {
                        return (
                          <select
                            key={actionIndex}
                            className="table__select"
                            value={action.value ?? ""}
                            aria-label={action.label}
                            onChange={(event) => {
                              const option = action.options?.find(
                                (item) => item.value === event.target.value,
                              );
                              event.target.value = action.value ?? "";
                              if (!option || option.value === action.value) return;
                              onAction({
                                label: action.label,
                                input: option.input,
                                confirm: option.confirm ?? action.confirm,
                              });
                            }}
                          >
                            {(action.options ?? []).map((option) => (
                              <option key={option.value} value={option.value}>
                                {option.label}
                              </option>
                            ))}
                          </select>
                        );
                      }
                      return (
                        <Button
                          key={actionIndex}
                          size="sm"
                          variant={actionTone(action.tone)}
                          onPress={() => onAction(action)}
                        >
                          {action.label}
                        </Button>
                      );
                    })}
                  </td>
                )}
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td className="table__cell" colSpan={columnCount}>
                  暂无数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {batched && (
        <div className="table__batch">
          <span>
            已选择 {selected.size} / {rows.length} 项
          </span>
          <Button
            variant={batchAction?.tone === "danger" ? "danger-soft" : "secondary"}
            size="sm"
            isDisabled={selected.size === 0}
            onPress={runBatch}
          >
            {batchAction?.label}
          </Button>
        </div>
      )}
    </div>
  );
}
