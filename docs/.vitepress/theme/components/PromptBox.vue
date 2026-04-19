<template>
  <div class="prompt-box">
    <p class="prompt-box__label">Paste this into any AI agent to get started:</p>
    <div class="prompt-box__inner">
      <code class="prompt-box__code">{{ START_PROMPT }}</code>
      <button class="prompt-box__btn" @click="copy" :class="{ copied }">
        {{ copied ? '✓ Copied' : 'Copy' }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { START_PROMPT } from '../prompt'

const copied = ref(false)

function copy() {
  navigator.clipboard.writeText(START_PROMPT)
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
