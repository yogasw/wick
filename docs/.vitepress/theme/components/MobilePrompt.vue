<template>
  <div class="mobile-prompt">
    <p class="mobile-prompt__label">Paste this into any AI agent to get started:</p>
    <div class="mobile-prompt__box">
      <code class="mobile-prompt__code">{{ prompt }}</code>
      <button class="mobile-prompt__copy" @click="copy" :class="{ copied }">
        {{ copied ? '✓ Copied' : 'Copy' }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { START_PROMPT } from '../prompt'

const prompt = START_PROMPT
const copied = ref(false)

function copy() {
  navigator.clipboard.writeText(prompt)
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}
</script>

<style scoped>
.mobile-prompt {
  display: none;
  margin-top: 24px;
  width: 100%;
}

.mobile-prompt__label {
  font-size: 11px;
  font-weight: 600;
  color: var(--vp-c-text-3);
  margin-bottom: 10px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.mobile-prompt__box {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  overflow: hidden;
  background: var(--vp-c-bg-soft);
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
}

.mobile-prompt__code {
  display: block;
  padding: 16px 18px;
  font-size: 12px;
  font-family: var(--vp-font-family-mono);
  white-space: pre-wrap;
  color: var(--vp-c-text-1);
  line-height: 1.75;
  text-align: left;
}

.mobile-prompt__copy {
  padding: 10px 16px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  border: none;
  border-top: 1px solid var(--vp-c-divider);
  background: transparent;
  color: var(--vp-c-brand-1);
  text-align: right;
  transition: background 0.15s;
}

.mobile-prompt__copy:hover { background: var(--vp-c-brand-soft); }
.mobile-prompt__copy.copied { color: var(--vp-c-green-1); }

@media (max-width: 959px) {
  .mobile-prompt {
    display: block;
  }
}
</style>
