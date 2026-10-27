<script setup lang="ts">
import type { StatusCell, StatusLegendItem, Tone } from "../types";

const props = defineProps<{
  cells: StatusCell[];
  legend?: StatusLegendItem[];
  label?: string;
  labeled?: boolean;
}>();

function toneClass(tone?: Tone) {
  switch (tone) {
    case "success":
      return "status-grid__cell--success";
    case "warning":
      return "status-grid__cell--warning";
    case "danger":
      return "status-grid__cell--danger";
    case "neutral":
      return "status-grid__cell--neutral";
    default:
      return "status-grid__cell--muted";
  }
}

function categoryClass(cell: StatusCell) {
  return cell.category ? `status-grid__cell--${cell.category}` : "";
}

function cellTip(cell: StatusCell) {
  return cell.detail ? `${cell.label} ${cell.detail}` : cell.label;
}
</script>

<template>
  <div class="status-grid" :aria-label="props.label ?? '状态网格'">
    <div v-if="props.legend?.length" class="status-grid__legend">
      <span v-for="item in props.legend" :key="item.label" class="status-grid__legend-item">
        <span
          class="status-grid__legend-dot"
          :class="item.category ? `status-grid__cell--${item.category}` : toneClass(item.tone)"
        />
        {{ item.label }}
      </span>
    </div>
    <div class="status-grid__cells" :class="{ 'status-grid__cells--labeled': props.labeled }">
      <span
        v-for="(cell, index) in props.cells"
        :key="`${cell.label}-${index}`"
        class="status-grid__cell"
        :class="[toneClass(cell.tone), categoryClass(cell)]"
        :title="cellTip(cell)"
      >
        <template v-if="props.labeled">
          <span class="status-grid__cell-label">{{ cell.label }}</span>
          <span v-if="cell.detail" class="status-grid__cell-detail">{{ cell.detail }}</span>
        </template>
      </span>
    </div>
  </div>
</template>
