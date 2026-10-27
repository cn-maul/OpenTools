<script setup lang="ts">
defineProps<{
  columns: string[];
  rows: string[][];
  label?: string;
  wide?: boolean;
}>();
</script>

<template>
  <div class="table-root table-root--primary">
    <div class="table__scroll-container" role="region" :aria-label="label" tabindex="0">
      <table
        class="table__content"
        :aria-label="label"
        :style="{ minWidth: wide ? `${Math.max(760, columns.length * 126)}px` : undefined }"
      >
        <thead class="table__header">
          <tr>
            <th v-for="column in columns" :key="column" class="table__column" scope="col">
              {{ column }}
            </th>
          </tr>
        </thead>
        <tbody class="table__body">
          <tr v-for="(row, rowIndex) in rows" :key="rowIndex" class="table__row">
            <td v-for="(cell, cellIndex) in row" :key="cellIndex" class="table__cell mono">
              {{ cell }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
