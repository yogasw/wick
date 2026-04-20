import DefaultTheme from 'vitepress/theme'
import './custom.css'
import HomeInstall from './components/HomeInstall.vue'
import MobilePrompt from './components/MobilePrompt.vue'
import PromptBox from './components/PromptBox.vue'
import Footer from './components/Footer.vue'
// @ts-ignore
import CopyOrDownloadAsMarkdownButtons from 'vitepress-plugin-llms/vitepress-components/CopyOrDownloadAsMarkdownButtons.vue'
import { h } from 'vue'
import type { App } from 'vue'

export default {
  extends: DefaultTheme,
  Layout() {
    return h(DefaultTheme.Layout, null, {
      'home-hero-image': () => h(HomeInstall),
      'home-hero-actions-after': () => h(MobilePrompt),
      'layout-bottom': () => h(Footer),
    })
  },
  enhanceApp({ app }: { app: App }) {
    app.component('PromptBox', PromptBox)
    app.component('CopyOrDownloadAsMarkdownButtons', CopyOrDownloadAsMarkdownButtons)
  },
}
