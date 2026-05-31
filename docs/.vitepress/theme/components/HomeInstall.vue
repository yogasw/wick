<template>
  <div class="home-install">
    <p class="home-install__label">Paste this into any AI agent to get started:</p>
    <div class="home-install__tabs" role="tablist">
      <button
        v-for="t in tabs"
        :key="t.id"
        :class="['home-install__tab', { active: active === t.id }]"
        role="tab"
        :aria-selected="active === t.id"
        @click="active = t.id"
      >
        {{ t.label }}
      </button>
    </div>
    <div class="home-install__box">
      <code class="home-install__code">{{ currentPrompt }}</code>
      <button class="home-install__btn" @click="copy" :class="{ copied }">
        {{ copied ? '✓ Copied' : 'Copy' }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { AGENT_PROMPT, FRAMEWORK_PROMPT } from '../prompt'

type TabId = 'agent' | 'framework'

const tabs: { id: TabId; label: string }[] = [
  { id: 'agent', label: 'Wick Agent' },
  { id: 'framework', label: 'Wick Framework' },
]

const active = ref<TabId>('agent')
const copied = ref(false)

const currentPrompt = computed(() =>
  active.value === 'agent' ? AGENT_PROMPT : FRAMEWORK_PROMPT,
)

function copy() {
  navigator.clipboard.writeText(currentPrompt.value)
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}
</script>

<style scoped>
.home-install {
  width: 100%;
  max-width: 500px;
  margin: 0 auto;
}

.home-install__label {
  font-size: 11px;
  font-weight: 600;
  color: var(--vp-c-text-3);
  margin-bottom: 10px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.home-install__tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 8px;
}

.home-install__tab {
  flex: 1;
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  background: transparent;
  color: var(--vp-c-text-2);
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.home-install__tab:hover {
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-text-1);
}

.home-install__tab.active {
  background: var(--vp-c-brand-soft);
  border-color: var(--vp-c-brand-1);
  color: var(--vp-c-brand-1);
}

.home-install__box {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  overflow: hidden;
  background: var(--vp-c-bg-soft);
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
}

.home-install__code {
  padding: 18px 20px;
  font-size: 12.5px;
  font-family: var(--vp-font-family-mono);
  white-space: pre-wrap;
  color: var(--vp-c-text-1);
  line-height: 1.75;
  text-align: left;
  display: block;
}

.home-install__btn {
  padding: 10px 20px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  border: none;
  border-top: 1px solid var(--vp-c-divider);
  background: transparent;
  color: var(--vp-c-brand-1);
  text-align: right;
  letter-spacing: 0.03em;
  transition: background 0.15s, color 0.15s;
}

.home-install__btn:hover { background: var(--vp-c-brand-soft); }
.home-install__btn.copied { color: var(--vp-c-green-1); }
</style>
