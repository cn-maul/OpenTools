import { useState } from "react";

import { Button, Checkbox, Switch, Table } from "@heroui/react";

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

type RowItem = {
  id: string;
  index: number;
  row: string[];
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

  const items: RowItem[] = rows.map((row, index) => ({
    id: String(index),
    index,
    row,
  }));

  return (
    <div className="table-root table-root--primary" data-wide={wide || undefined}>
      <Table.ScrollContainer
        className="table__scroll-container"
        role="region"
        aria-label={label}
        tabIndex={0}
      >
        <Table.Content aria-label={label}>
          <Table.Header>
            {batched && (
              <Table.Column id="select" className="table__column">
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
              </Table.Column>
            )}
            {columns.map((column, index) => (
              <Table.Column
                key={`col-${column}-${index}`}
                id={`col-${index}`}
                isRowHeader={index === 0}
                className="table__column"
                style={{ width: columnWidths?.[index] }}
              >
                {column}
              </Table.Column>
            ))}
            {hasActions && (
              <Table.Column id="actions" className="table__column">
                {actionsHeader}
              </Table.Column>
            )}
          </Table.Header>
          <Table.Body
            items={items}
            renderEmptyState={() => <span className="app-table-empty">暂无数据</span>}
          >
            {(item) => (
              <Table.Row id={item.id} className="table__row">
                {batched && (
                  <Table.Cell id={`${item.id}-select`} className="table__cell table__cell--select">
                    <Checkbox
                      aria-label={`选择第 ${item.index + 1} 行`}
                      isSelected={selected.has(item.index)}
                      onChange={(checked) => toggleRow(item.index, checked)}
                    >
                      <Checkbox.Content>
                        <Checkbox.Control>
                          <Checkbox.Indicator />
                        </Checkbox.Control>
                      </Checkbox.Content>
                    </Checkbox>
                  </Table.Cell>
                )}
                {item.row.map((cell, cellIndex) => {
                  const truncate = truncateColumn(columns[cellIndex] ?? "");
                  return (
                    <Table.Cell
                      key={`${item.id}-c${cellIndex}`}
                      id={`${item.id}-c${cellIndex}`}
                      className="table__cell mono"
                    >
                      {cellIndex === 0 && rowIcons?.length ? (
                        <span className="table__cell-with-icon">
                          <span className="table__row-icon" aria-hidden="true">
                            {rowIcons[item.index] ? (
                              <img src={rowIcons[item.index]} alt="" />
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
                    </Table.Cell>
                  );
                })}
                {hasActions && (
                  <Table.Cell id={`${item.id}-actions`} className="table__cell table__cell--actions">
                    {(rowActions?.[item.index] ?? []).map((action, actionIndex) => {
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
                                (optionItem) => optionItem.value === event.target.value,
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
                  </Table.Cell>
                )}
              </Table.Row>
            )}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
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
