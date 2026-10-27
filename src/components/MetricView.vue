<script setup lang="ts">
import { computed } from "vue";

import type { FactItem, MeterItem, Tone } from "../types";

const props = defineProps<{
  metrics?: MeterItem[];
  facts?: FactItem[];
  label?: string;
}>();

const rings = computed(() =>
  (props.metrics ?? []).filter((item) => (item.variant ?? "ring") === "ring"),
);
const bars = computed(() => (props.metrics ?? []).filter((item) => item.variant === "bar"));

const groupedFacts = computed(() => {
  const groups: { name: string; items: FactItem[] }[] = [];
  for (const fact of props.facts ?? []) {
    const name = fact.group ?? "";
    const existing = groups.find((group) => group.name === name);
    if (existing) existing.items.push(fact);
    else groups.push({ name, items: [fact] });
  }
  return groups;
});

function toneVar(tone?: Tone) {
  switch (tone) {
    case "success":
      return "var(--success)";
    case "warning":
      return "var(--warning)";
    case "danger":
      return "var(--danger)";
    default:
      return "var(--accent)";
  }
}

function ratio(item: MeterItem) {
  const max = item.max && item.max > 0 ? item.max : 100;
  return Math.max(0, Math.min(1, item.value / max));
}

function ringStyle(item: MeterItem) {
  return {
    "--meter-color": toneVar(item.tone),
    "--meter-angle": `${ratio(item) * 360}deg`,
  };
}

function barStyle(item: MeterItem) {
  return {
    "--meter-color": toneVar(item.tone),
    "--meter-ratio": `${ratio(item) * 100}%`,
  };
}

function display(item: MeterItem) {
  const rounded = Number.isInteger(item.value) ? item.value : Math.round(item.value * 10) / 10;
  return `${rounded}${item.unit ?? (item.max === 100 ? "%" : "")}`;
}

function segmentStyle(value: number, item: MeterItem) {
  const max = item.max && item.max > 0 ? item.max : 100;
  return { width: `${Math.max(0, Math.min(100, (value / max) * 100))}%` };
}
</script>

<template>
  <div class="metric-view" :aria-label="props.label ?? '指标'">
    <div v-if="rings.length" class="metric-rings">
      <div v-for="item in rings" :key="item.label" class="metric-ring" :style="ringStyle(item)">
        <div class="metric-ring__dial">
          <span class="metric-ring__value">{{ display(item) }}</span>
        </div>
        <span class="metric-ring__label">{{ item.label }}</span>
        <span v-if="item.detail" class="metric-ring__detail">{{ item.detail }}</span>
      </div>
    </div>

    <div v-if="bars.length" class="metric-bars">
      <div v-for="item in bars" :key="item.label" class="metric-bar" :style="barStyle(item)">
        <div class="metric-bar__head">
          <span class="metric-bar__label">{{ item.label }}</span>
          <span class="metric-bar__value">{{ item.detail ?? display(item) }}</span>
        </div>
        <div class="metric-bar__track">
          <span v-if="item.segments?.length" class="metric-bar__segments">
            <span
              v-for="segment in item.segments"
              :key="segment.label"
              class="metric-bar__segment"
              :style="{ ...segmentStyle(segment.value, item), background: toneVar(segment.tone) }"
              :title="segment.label"
            />
          </span>
          <span v-else class="metric-bar__fill" />
        </div>
      </div>
    </div>

    <div v-if="groupedFacts.length" class="metric-facts">
      <div v-for="group in groupedFacts" :key="group.name" class="metric-fact-group">
        <span v-if="group.name" class="metric-fact-group__name">{{ group.name }}</span>
        <dl class="metric-fact-list">
          <div v-for="fact in group.items" :key="`${group.name}-${fact.label}`" class="metric-fact">
            <dt>{{ fact.label }}</dt>
            <dd>{{ fact.value }}</dd>
          </div>
        </dl>
      </div>
    </div>
  </div>
</template>

<style scoped>
.metric-view {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.metric-rings {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(118px, 1fr));
  gap: 14px;
}

.metric-ring {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  border: 1px solid color-mix(in oklab, var(--border) 60%, transparent);
  border-radius: 16px;
  background: linear-gradient(
    160deg,
    color-mix(in oklab, var(--meter-color) 9%, var(--surface)) 0%,
    var(--surface) 62%
  );
  padding: 16px 12px 13px;
  text-align: center;
}

.metric-ring__dial {
  display: grid;
  width: 78px;
  height: 78px;
  place-items: center;
  border-radius: 50%;
  background: conic-gradient(
    var(--meter-color) 0 var(--meter-angle),
    color-mix(in oklab, var(--foreground) 10%, transparent) var(--meter-angle) 360deg
  );
}

.metric-ring__dial::before {
  grid-area: 1 / 1;
  width: 62px;
  height: 62px;
  border-radius: 50%;
  background: var(--surface);
  content: "";
}

.metric-ring__value {
  grid-area: 1 / 1;
  font-family: var(--font-mono);
  font-size: 17px;
  font-weight: 650;
  letter-spacing: -0.02em;
}

.metric-ring__label {
  font-size: 13px;
  font-weight: 600;
}

.metric-ring__detail {
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.4;
}

.metric-bars {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.metric-bar__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
}

.metric-bar__label {
  font-size: 13px;
  font-weight: 600;
}

.metric-bar__value {
  color: var(--muted);
  font-family: var(--font-mono);
  font-size: 12px;
}

.metric-bar__track {
  position: relative;
  height: 9px;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in oklab, var(--foreground) 10%, transparent);
}

.metric-bar__fill {
  display: block;
  width: var(--meter-ratio);
  height: 100%;
  border-radius: inherit;
  background: var(--meter-color);
}

.metric-bar__segments {
  display: flex;
  height: 100%;
  gap: 2px;
}

.metric-bar__segment {
  height: 100%;
}

.metric-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 10px 16px;
}

.metric-fact-group {
  min-width: 0;
}

.metric-fact-group__name {
  display: block;
  margin-bottom: 7px;
  color: var(--muted);
  font-family: var(--font-mono);
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

.metric-fact-list {
  display: flex;
  margin: 0;
  flex-direction: column;
  gap: 1px;
}

.metric-fact {
  display: grid;
  grid-template-columns: minmax(74px, auto) minmax(0, 1fr);
  align-items: baseline;
  gap: 10px;
  border-radius: 8px;
  padding: 5px 8px;
}

.metric-fact:nth-child(odd) {
  background: color-mix(in oklab, var(--foreground) 4%, transparent);
}

.metric-fact dt {
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1.45;
}

.metric-fact dd {
  min-width: 0;
  margin: 0;
  font-size: 13px;
  line-height: 1.45;
  text-align: right;
  overflow-wrap: anywhere;
}
</style>
