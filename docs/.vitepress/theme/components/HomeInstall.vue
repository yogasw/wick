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
