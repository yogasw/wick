<template>
  <div class="home-install">
    <p class="home-install__label">Paste this into any AI agent to get started:</p>
    <div class="home-install__box">
      <code class="home-install__code">{{ prompt }}</code>
      <button class="home-install__btn" @click="copy" :class="{ copied }">
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
.home-install {
  width: 100%;
  max-width: 420px;
}

.home-install__label {
  font-size: 12px;
  color: var(--vp-c-text-2);
  margin-bottom: 10px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.home-install__box {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  overflow: hidden;
  background: var(--vp-c-bg-soft);
}

.home-install__code {
  padding: 16px;
  font-size: 12.5px;
  font-family: var(--vp-font-family-mono);
  white-space: pre-wrap;
  color: var(--vp-c-text-1);
  line-height: 1.7;
}

.home-install__btn {
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

.home-install__btn:hover {
  background: var(--vp-c-brand-soft);
}

.home-install__btn.copied {
  color: var(--vp-c-green-1);
}
</style>
