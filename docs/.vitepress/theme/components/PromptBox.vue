<template>
  <div class="prompt-box">
    <p class="prompt-box__label">Paste this into any AI agent to get started:</p>
    <div class="prompt-box__tabs" role="tablist">
      <button
        v-for="t in tabs"
        :key="t.id"
        :class="['prompt-box__tab', { active: active === t.id }]"
        role="tab"
        :aria-selected="active === t.id"
        @click="active = t.id"
      >
        {{ t.label }}
      </button>
    </div>
    <div class="prompt-box__inner">
      <code class="prompt-box__code">{{ currentPrompt }}</code>
      <button class="prompt-box__btn" @click="copy" :class="{ copied }">
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
.prompt-box { margin: 24px 0; }

.prompt-box__label {
  font-size: 12px;
  color: var(--vp-c-text-2);
  margin-bottom: 8px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.prompt-box__tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 8px;
}

.prompt-box__tab {
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

.prompt-box__tab:hover {
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-text-1);
}

.prompt-box__tab.active {
  background: var(--vp-c-brand-soft);
  border-color: var(--vp-c-brand-1);
  color: var(--vp-c-brand-1);
}

.prompt-box__inner {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  overflow: hidden;
  background: var(--vp-c-bg-soft);
}

.prompt-box__code {
  padding: 16px;
  font-size: 13px;
  font-family: var(--vp-font-family-mono);
  white-space: pre-wrap;
  color: var(--vp-c-text-1);
  line-height: 1.7;
}

.prompt-box__btn {
  padding: 10px 16px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  border: none;
  border-top: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-brand-1);
  text-align: right;
  transition: background 0.15s, color 0.15s;
}

.prompt-box__btn:hover { background: var(--vp-c-brand-soft); }
.prompt-box__btn.copied { color: var(--vp-c-green-1); }
</style>
